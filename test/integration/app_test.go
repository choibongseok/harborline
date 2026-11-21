//go:build integration

package integration

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choibongseok/harborline/internal/config"
	"github.com/choibongseok/harborline/internal/llm/mock"
	"github.com/choibongseok/harborline/internal/mocksmtp"
	"github.com/choibongseok/harborline/internal/pki"
)

type site struct {
	*env
	SMTP *mocksmtp.Server
	LLM  *mock.Server
	Cfg  config.Config
}

// newSite 는 실제 PG + 사설 CA 로 STARTTLS 를 여는 SMTP 모의 + 내부 LLM 모의를 묶어 설치형 프로필 서버를 띄운다.
func newSite(t *testing.T, features string, migrations ...string) *site {
	t.Helper()
	db := newDB(t)
	applyMigrations(t, db, migrations...)
	ca, err := pki.NewCA("corp-ca", time.Now().Add(24*time.Hour))
	must(t, err)
	cert, err := ca.Issue("localhost", "127.0.0.1")
	must(t, err)
	sm := &mocksmtp.Server{TLS: &tls.Config{Certificates: []tls.Certificate{cert}}, RequireTLS: true}
	must(t, sm.Listen("127.0.0.1:0"))
	t.Cleanup(sm.Close)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	must(t, os.WriteFile(caFile, ca.PEM, 0o600))
	prof, _ := mock.Lookup("full")
	ms := &mock.Server{P: prof}
	llmSrv := httptest.NewServer(ms)
	t.Cleanup(llmSrv.Close)

	cfg := baseConfig(db)
	cfg.Mail.SMTP = config.SMTP{Host: "localhost", Port: portOf(sm.Addr()), StartTLS: true}
	cfg.HTTP.CAFile = caFile
	cfg.LLM = config.LLM{Kind: "internal", BaseURL: llmSrv.URL, Model: "internal-model"}
	return &site{env: start(t, cfg, features, "", nil), SMTP: sm, LLM: ms, Cfg: cfg}
}

func TestInquiryFlowOnRealPostgresWithOnpremAdapters(t *testing.T) {
	s := newSite(t, "stats", "0001_init.sql")
	if code, _, _ := s.do(t, "GET", "/api/inquiries", nil); code != 401 {
		t.Fatalf("로그인 전 목록은 401: %d", code)
	}
	id := s.createInquiry(t, "환불 문의", "인보이스 refund 부탁드립니다. 긴급")
	if s.login(t, "admin", "admin-pw") != 200 {
		t.Fatal("시드된 관리자로 로그인되어야 함")
	}
	code, m, raw := s.do(t, "GET", "/api/inquiries/"+id, nil)
	if code != 200 || m["status"] != "open" {
		t.Fatalf("%d %s", code, raw)
	}
	// 답변 → STARTTLS(사설 CA)로 메일이 가고 상태가 answered 로 바뀐다
	code, m, raw = s.do(t, "POST", "/api/inquiries/"+id+"/replies", map[string]string{"body": "확인했습니다"})
	if code != 201 || m["mailed"] != true {
		t.Fatalf("%d %s", code, raw)
	}
	mails := s.SMTP.Mails()
	if len(mails) != 1 || !mails[0].TLS || mails[0].To != "customer@example.test" {
		t.Fatalf("고객 SMTP 로 TLS 메일이 한 통 가야 함: %+v", mails)
	}
	_, m, _ = s.do(t, "GET", "/api/inquiries/"+id, nil)
	if m["status"] != "answered" {
		t.Fatalf("상태: %v", m["status"])
	}
	if _, st, _ := s.do(t, "GET", "/api/stats", nil); st["answered"] != float64(1) {
		t.Fatalf("v1.1 통계: %v", st)
	}
}

func TestAttachmentRoundTripOnLocalDisk(t *testing.T) {
	s := newSite(t, "", "0001_init.sql")
	id := s.createInquiry(t, "첨부", "파일을 보냅니다")
	data := strings.Repeat("log line\n", 1000)
	code, m, raw := s.do(t, "POST", "/api/inquiries/"+id+"/attachments?name=app.log", data)
	if code != 201 {
		t.Fatalf("%d %s", code, raw)
	}
	sum := sha256.Sum256([]byte(data))
	if m["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("업로드 체크섬: %v", m["sha256"])
	}
	s.login(t, "admin", "admin-pw")
	resp, err := s.Client.Get(s.URL + "/api/attachments/" + m["id"].(string))
	must(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != data {
		t.Fatal("내려받은 첨부가 올린 것과 다름")
	}
	if code, _, _ := s.do(t, "POST", "/api/inquiries/"+id+"/attachments?name=../etc/passwd", "x"); code != 422 {
		t.Fatalf("경로 문자가 든 파일 이름은 거부: %d", code)
	}
}

func TestDraftSuggestionUsesInternalModelEndpoint(t *testing.T) {
	s := newSite(t, "", "0001_init.sql")
	id := s.createInquiry(t, "로그인 오류", "password 를 바꿨는데 로그인이 안 됩니다")
	s.login(t, "admin", "admin-pw")
	code, m, raw := s.do(t, "POST", "/api/inquiries/"+id+"/draft", nil)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	cls := m["classification"].(map[string]any)
	dr := m["draft"].(map[string]any)
	if cls["category"] != "account" || cls["path"] != "tools" || !strings.Contains(dr["text"].(string), "DRAFT(account)") {
		t.Fatalf("%v", m)
	}
	if _, sys, _ := s.do(t, "GET", "/api/system", nil); sys["profile"] != "onprem" {
		t.Fatalf("%v", sys)
	}
}

func TestDraftIsUnavailableButServiceKeepsWorkingWhenModelEndpointIsDown(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	cfg := baseConfig(db)
	cfg.LLM = config.LLM{Kind: "internal", BaseURL: "http://127.0.0.1:1", Model: "x"}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	l.Close()
	e := start(t, cfg, "", "", &http.Client{Timeout: 2 * time.Second})
	id := e.createInquiry(t, "문의", "내용")
	e.login(t, "admin", "admin-pw")
	if code, _, _ := e.do(t, "POST", "/api/inquiries/"+id+"/draft", nil); code != 503 {
		t.Fatalf("초안 기능만 503 이어야 함: %d", code)
	}
	if code, _, _ := e.do(t, "GET", "/api/inquiries/"+id, nil); code != 200 {
		t.Fatalf("나머지 기능은 정상이어야 함: %d", code)
	}
}

func TestHealthzReportsVersionAndSchemaAndFailsWhenBuildIsBroken(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql", "0002_inquiry_state.sql")
	cfg := baseConfig(db)
	ok := start(t, cfg, "stats,state", "", nil)
	code, m, _ := ok.do(t, "GET", "/healthz", nil)
	if code != 200 || m["version"] != "v-test" || m["schema"] != "0002_inquiry_state" {
		t.Fatalf("%d %v", code, m)
	}
	bad := start(t, cfg, "stats,state", "health", nil)
	if code, _, _ := bad.do(t, "GET", "/healthz", nil); code != 503 {
		t.Fatalf("의도적으로 깨진 빌드는 503: %d", code)
	}
}
