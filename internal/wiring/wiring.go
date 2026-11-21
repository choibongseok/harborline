// Package wiring 은 설정에서 어댑터를 골라 코어에 꽂는 유일한 자리(composition root)다.
// 설치형 프로필에서는 클라우드 어댑터(S3·메일 API·호스팅 LLM)를 생성하는 코드 경로에 들어가지 않는다.
package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/choibongseok/harborline/internal/adapters/auth"
	"github.com/choibongseok/harborline/internal/adapters/mail"
	"github.com/choibongseok/harborline/internal/adapters/storage"
	"github.com/choibongseok/harborline/internal/app"
	"github.com/choibongseok/harborline/internal/config"
	"github.com/choibongseok/harborline/internal/httpx"
	"github.com/choibongseok/harborline/internal/llm"
	"github.com/choibongseok/harborline/internal/migrate"
	"github.com/choibongseok/harborline/internal/store"
)

// Options 는 테스트가 바꿔 끼우는 부분이다.
type Options struct {
	HTTP *http.Client // 비면 설정(프록시·CA)으로 만든다
	Log  *slog.Logger
}

// ParseFeatures 는 "stats,state" 를 읽는다.
func ParseFeatures(s string) store.Features {
	var f store.Features
	for _, k := range strings.Split(s, ",") {
		switch strings.TrimSpace(k) {
		case "stats":
			f.Stats = true
		case "state":
			f.State = true
		case "state-only":
			f.State, f.StateOnly = true, true
		}
	}
	return f
}

// Build 는 설정대로 의존성을 만든다. 호출자가 반환된 close 를 호출해야 한다.
func Build(ctx context.Context, cfg config.Config, features, breakMode string, o Options) (*app.Server, func(), error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("설정 오류: %w", err)
	}
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	hc := o.HTTP
	if hc == nil {
		var err error
		hc, err = httpx.New(httpx.Options{ProxyURL: cfg.HTTP.ProxyURL, NoProxy: cfg.HTTP.NoProxy, CAFile: cfg.HTTP.CAFile})
		if err != nil {
			return nil, nil, err
		}
	}
	roots, err := httpx.CertPool(cfg.HTTP.CAFile)
	if err != nil {
		return nil, nil, err
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("db 연결 설정: %w", err)
	}
	st := &store.Store{Pool: pool, F: ParseFeatures(features)}
	sess := &auth.Sessions{Secret: []byte(cfg.SessionSecret)}
	d := app.Deps{Store: st, Sessions: sess, Profile: cfg.Profile, Version: cfg.Version, Features: st.F, MailFrom: cfg.Mail.From, Break: breakMode, Log: log,
		SchemaVersion: func(ctx context.Context) string { return migrate.Latest(ctx, pool) }}

	// 저장소
	switch cfg.Storage.Kind {
	case "disk":
		d.Storage, err = storage.NewDisk(cfg.Storage.Dir)
		if err != nil {
			pool.Close()
			return nil, nil, err
		}
	case "s3":
		s := cfg.Storage.S3
		d.Storage = &storage.S3{Endpoint: s.Endpoint, Bucket: s.Bucket, Region: s.Region, AccessKey: s.AccessKey, SecretKey: s.SecretKey, Client: hc}
	}
	// 메일
	switch cfg.Mail.Kind {
	case "smtp":
		m := cfg.Mail.SMTP
		d.Mail = &mail.SMTP{Host: m.Host, Port: m.Port, StartTLS: m.StartTLS, Username: m.Username, Password: m.Password, Roots: roots}
	case "api":
		d.Mail = &mail.API{URL: cfg.Mail.API.URL, Token: cfg.Mail.API.Token, Client: hc}
	}
	// 인증
	switch cfg.Auth.Kind {
	case "local":
		d.Auth = &auth.Local{Users: st, Sessions: sess}
		if cfg.AdminPassword != "" {
			h, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
			if err != nil {
				pool.Close()
				return nil, nil, err
			}
			if err := st.UpsertUser(ctx, "admin", string(h), auth.RoleAdmin); err != nil {
				log.Warn("관리자 계정 시드 실패(스키마가 아직 없으면 정상)", "err", err)
			}
		}
	case "ldap":
		l := cfg.Auth.LDAP
		d.Auth = &auth.LDAP{URL: l.URL, BindDN: l.BindDN, BindPassword: l.BindPassword, UserBaseDN: l.UserBaseDN, UserFilter: l.UserFilter,
			GroupBaseDN: l.GroupBaseDN, GroupFilter: l.GroupFilter, StartTLS: l.StartTLS, Roots: roots, RoleMap: l.RoleMap, Sessions: sess}
	case "oidc":
		o := cfg.Auth.OIDC
		var oerr error
		var oa *auth.OIDC
		for i := 0; i < 15; i++ { // 공급자가 늦게 뜰 수 있어 잠시 재시도
			oa, oerr = auth.NewOIDC(ctx, hc, o.Issuer, o.ClientID, o.ClientSecret, o.RedirectURL, o.GroupsClaim, o.RoleMap, sess)
			if oerr == nil {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
		if oerr != nil {
			pool.Close()
			return nil, nil, oerr
		}
		d.Auth = oa
	}
	// LLM
	d.LLMKind = cfg.LLM.Kind
	if cfg.LLM.Kind != "off" {
		c := &llm.Client{BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey, Model: cfg.LLM.Model, HTTP: hc}
		pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		dr := llm.NewDrafter(pctx, c)
		cancel()
		d.LLMCaps = dr.Caps
		if dr.Caps.Reachable {
			d.Drafter = dr
		} else {
			log.Warn("LLM 엔드포인트에 닿지 않아 답변 초안 기능을 끈다", "notes", dr.Caps.Notes)
		}
		log.Info("LLM 기능 탐지", "tools", dr.Caps.Tools, "json_mode", dr.Caps.JSONMode, "context_tokens", dr.Caps.ContextTokens, "classify_path", dr.ClassifyPath())
	}
	return &app.Server{D: d}, pool.Close, nil
}
