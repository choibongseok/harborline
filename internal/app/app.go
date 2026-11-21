// Package app 는 문의함 서비스의 HTTP 계층이다. 외부 의존성은 어댑터 인터페이스로만 만난다.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/choibongseok/harborline/internal/adapters/auth"
	mailer "github.com/choibongseok/harborline/internal/adapters/mail"
	"github.com/choibongseok/harborline/internal/adapters/storage"
	"github.com/choibongseok/harborline/internal/llm"
	"github.com/choibongseok/harborline/internal/store"
)

// Drafter 는 답변 초안 기능이다(꺼져 있을 수 있다).
type Drafter interface {
	ClassifyPath() string
	Classify(ctx context.Context, text string) (llm.Classification, error)
	DraftReply(ctx context.Context, history, last string) (llm.Draft, error)
}

// Deps 는 서버가 쓰는 모든 외부 의존성이다.
type Deps struct {
	Store    *store.Store
	Storage  storage.Store
	Mail     mailer.Sender
	Auth     auth.Authenticator
	Sessions *auth.Sessions
	Drafter  Drafter  // nil 이면 비활성
	LLMCaps  llm.Caps // 탐지 결과(표시용)
	LLMKind  string
	Profile  string
	Version  string
	Features store.Features
	MailFrom string
	Break    string // 빌드 시 주입한 의도적 결함(검증용): "health"
	Log      *slog.Logger
	// 마지막 마이그레이션 이름을 알려 주는 함수(상태 점검 표시용)
	SchemaVersion func(ctx context.Context) string
}

// Server 는 라우터다.
type Server struct{ D Deps }

const maxBody = 10 << 20

// Handler 는 전체 라우트를 돌려준다.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/system", s.system)
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("POST /api/inquiries", s.createInquiry)
	mux.HandleFunc("POST /api/inquiries/{id}/attachments", s.addAttachment)
	mux.HandleFunc("GET /api/inquiries", s.agent(s.listInquiries))
	mux.HandleFunc("GET /api/inquiries/{id}", s.agent(s.getInquiry))
	mux.HandleFunc("POST /api/inquiries/{id}/replies", s.agent(s.addReply))
	mux.HandleFunc("POST /api/inquiries/{id}/draft", s.agent(s.draft))
	mux.HandleFunc("GET /api/attachments/{id}", s.agent(s.getAttachment))
	if s.D.Features.Stats {
		mux.HandleFunc("GET /api/stats", s.agent(s.stats))
	}
	s.mountOIDC(mux)
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) agent(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.D.Auth.Authenticate(r)
		if err != nil {
			code := http.StatusUnauthorized
			if errors.Is(err, auth.ErrNoRole) {
				code = http.StatusForbidden
			}
			writeErr(w, code, err.Error())
			return
		}
		if !p.HasRole(auth.RoleAgent) {
			writeErr(w, http.StatusForbidden, "agent 역할이 필요함")
			return
		}
		h(w, r, p)
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if s.D.Break == "health" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "version": s.D.Version, "reason": "의도적으로 깨진 릴리스"})
		return
	}
	if err := s.D.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "version": s.D.Version, "reason": "db: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.D.Version, "schema": s.D.SchemaVersion(ctx)})
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.D.Version, "profile": s.D.Profile,
		"adapters": map[string]string{"storage": s.D.Storage.Kind(), "mail": s.D.Mail.Kind(), "auth": s.D.Auth.Kind(), "llm": s.D.LLMKind},
		"llm":      map[string]any{"caps": s.D.LLMCaps, "classify_path": classifyPath(s.D.Drafter)},
		"features": s.D.Features,
	})
}

func classifyPath(d Drafter) string {
	if d == nil {
		return llm.PathDisabled
	}
	return d.ClassifyPath()
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.D.Auth.(auth.PasswordLogin)
	if !ok {
		writeErr(w, http.StatusNotFound, "이 인증 방식은 비밀번호 로그인을 쓰지 않음")
		return
	}
	var in struct{ Username, Password string }
	if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "잘못된 요청")
		return
	}
	p, err := pl.Login(r.Context(), in.Username, in.Password)
	switch {
	case errors.Is(err, auth.ErrNoRole):
		writeErr(w, http.StatusForbidden, "로그인은 됐지만 매핑된 역할이 없음")
		return
	case err != nil && errors.Is(err, auth.ErrUnauthenticated):
		writeErr(w, http.StatusUnauthorized, "아이디 또는 비밀번호가 맞지 않음")
		return
	case err != nil:
		s.D.Log.Error("로그인 처리 실패", "err", err)
		writeErr(w, http.StatusBadGateway, "인증 서버와 통신하지 못함")
		return
	}
	s.D.Sessions.SetCookie(w, p)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) mountOIDC(mux *http.ServeMux) {
	o, ok := s.D.Auth.(*auth.OIDC)
	if !ok {
		return
	}
	mux.HandleFunc("GET /auth/login", o.LoginRedirect)
	mux.HandleFunc("GET /auth/callback", func(w http.ResponseWriter, r *http.Request) {
		p, err := o.Callback(w, r)
		if err != nil {
			code := http.StatusUnauthorized
			if errors.Is(err, auth.ErrNoRole) {
				code = http.StatusForbidden
			}
			writeErr(w, code, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
}

func (s *Server) createInquiry(w http.ResponseWriter, r *http.Request) {
	var in struct{ Subject, Body, Email string }
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "잘못된 요청")
		return
	}
	in.Subject, in.Body = strings.TrimSpace(in.Subject), strings.TrimSpace(in.Body)
	if _, err := mail.ParseAddress(in.Email); err != nil || in.Subject == "" || in.Body == "" || len(in.Subject) > 200 {
		writeErr(w, http.StatusUnprocessableEntity, "subject, body, 올바른 email 이 필요함")
		return
	}
	id, err := s.D.Store.CreateInquiry(r.Context(), in.Subject, in.Body, in.Email)
	if err != nil {
		s.D.Log.Error("문의 저장 실패", "err", err)
		writeErr(w, http.StatusInternalServerError, "저장하지 못함")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) listInquiries(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.D.Store.ListInquiries(r.Context(), r.URL.Query().Get("status"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "조회하지 못함")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getInquiry(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	in, err := s.D.Store.GetInquiry(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "없는 문의")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "조회하지 못함")
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// addReply 는 답변을 저장하고 고객에게 메일을 보낸다. 메일 실패는 답변 저장을 되돌리지 않고 응답에 표시한다.
func (s *Server) addReply(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct{ Body string }
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in) != nil || strings.TrimSpace(in.Body) == "" {
		writeErr(w, http.StatusUnprocessableEntity, "body 가 필요함")
		return
	}
	id := r.PathValue("id")
	inq, err := s.D.Store.GetInquiry(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "없는 문의")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "조회하지 못함")
		return
	}
	if err := s.D.Store.AddReply(r.Context(), id, p.Name, in.Body); err != nil {
		writeErr(w, http.StatusInternalServerError, "저장하지 못함")
		return
	}
	mailed := true
	if err := s.D.Mail.Send(r.Context(), mailer.Message{From: s.D.MailFrom, To: inq.CustomerEmail, Subject: "Re: " + inq.Subject, Body: in.Body}); err != nil {
		mailed = false
		s.D.Log.Error("답변 메일 발송 실패", "err", err, "inquiry", id)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"mailed": mailed})
}

func (s *Server) addAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.URL.Query().Get("name")
	if name == "" || len(name) > 200 || strings.ContainsAny(name, "/\\\x00") {
		writeErr(w, http.StatusUnprocessableEntity, "name 이 필요함(경로 문자 불가)")
		return
	}
	if _, err := s.D.Store.GetInquiry(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, "없는 문의")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "첨부는 10MB 까지")
		return
	}
	sum := sha256.Sum256(data)
	key := id + "/" + hex.EncodeToString(sum[:8]) + "-" + name
	if err := s.D.Storage.Put(r.Context(), key, data); err != nil {
		s.D.Log.Error("첨부 저장 실패", "err", err)
		writeErr(w, http.StatusBadGateway, "첨부를 저장하지 못함")
		return
	}
	aid, err := s.D.Store.AddAttachment(r.Context(), id, name, int64(len(data)), hex.EncodeToString(sum[:]), key)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "저장하지 못함")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": aid, "sha256": hex.EncodeToString(sum[:]), "size_bytes": len(data)})
}

func (s *Server) getAttachment(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	a, err := s.D.Store.GetAttachment(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "없는 첨부")
		return
	}
	rc, err := s.D.Storage.Get(r.Context(), a.StorageKey)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "저장소에서 읽지 못함")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", a.Name))
	_, _ = io.Copy(w, rc)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	m, err := s.D.Store.Stats(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "조회하지 못함")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// draft 는 분류와 답변 초안을 제안한다. LLM 이 꺼져 있으면 503 으로 알리고 나머지 기능에는 영향이 없다.
func (s *Server) draft(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	if s.D.Drafter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "답변 초안 기능이 꺼져 있음", "path": llm.PathDisabled})
		return
	}
	inq, err := s.D.Store.GetInquiry(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "없는 문의")
		return
	}
	var hist strings.Builder
	for _, rp := range inq.Replies {
		hist.WriteString("Agent: " + rp.Body + "\n")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out := map[string]any{}
	cls, cerr := s.D.Drafter.Classify(ctx, inq.Subject+"\n"+inq.Body)
	if cerr != nil {
		out["classification"] = map[string]string{"error": cerr.Error(), "path": cls.Path}
	} else {
		out["classification"] = cls
		if r.URL.Query().Get("apply") == "true" {
			_ = s.D.Store.SetPriority(ctx, inq.ID, cls.Priority)
		}
	}
	dr, derr := s.D.Drafter.DraftReply(ctx, hist.String(), inq.Subject+"\n"+inq.Body)
	if derr != nil {
		out["draft"] = map[string]string{"error": derr.Error(), "path": dr.Path}
	} else {
		out["draft"] = dr
	}
	if cerr != nil && derr != nil {
		writeJSON(w, http.StatusServiceUnavailable, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
