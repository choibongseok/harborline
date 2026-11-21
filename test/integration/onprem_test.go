//go:build integration

package integration

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/choibongseok/harborline/internal/config"
	"github.com/choibongseok/harborline/internal/llm/mock"
	"github.com/choibongseok/harborline/internal/mocksmtp"
	"github.com/choibongseok/harborline/internal/pki"
	"github.com/choibongseok/harborline/internal/wiring"
)

// recorder 는 나가는 HTTP 요청의 목적지를 기록하는 RoundTripper 다.
type recorder struct {
	mu    sync.Mutex
	hosts map[string]int
	next  http.RoundTripper
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.hosts[req.URL.Host]++
	r.mu.Unlock()
	return r.next.RoundTrip(req)
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for h := range r.hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func runScenario(t *testing.T, e *env) {
	id := e.createInquiry(t, "환불 문의", "refund 요청")
	e.login(t, "admin", "admin-pw")
	e.do(t, "POST", "/api/inquiries/"+id+"/attachments?name=a.txt", "첨부")
	e.do(t, "POST", "/api/inquiries/"+id+"/draft", nil)
	e.do(t, "POST", "/api/inquiries/"+id+"/replies", map[string]string{"body": "답변"})
}

// 설치형 프로필: HTTP 로 나가는 곳은 설정한 내부 모델 서버뿐이다(S3·메일 API·호스팅 LLM 경로가 없다).
func TestOnpremProfileMakesNoOutboundHTTPOtherThanTheConfiguredInternalModel(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	ca, _ := pki.NewCA("corp-ca", time.Now().Add(time.Hour))
	cert, _ := ca.Issue("localhost")
	sm := &mocksmtp.Server{TLS: &tls.Config{Certificates: []tls.Certificate{cert}}}
	must(t, sm.Listen("127.0.0.1:0"))
	defer sm.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	must(t, os.WriteFile(caFile, ca.PEM, 0o600))
	prof, _ := mock.Lookup("full")
	llm := httptest.NewServer(&mock.Server{P: prof})
	defer llm.Close()

	rec := &recorder{hosts: map[string]int{}, next: http.DefaultTransport}
	cfg := baseConfig(db)
	cfg.Mail.SMTP = config.SMTP{Host: "localhost", Port: portOf(sm.Addr()), StartTLS: true}
	cfg.HTTP.CAFile = caFile
	cfg.LLM = config.LLM{Kind: "internal", BaseURL: llm.URL, Model: "m"}
	e := start(t, cfg, "", "", &http.Client{Transport: rec})
	runScenario(t, e)

	want := strings.TrimPrefix(llm.URL, "http://")
	if got := rec.list(); len(got) != 1 || got[0] != want {
		t.Fatalf("나간 HTTP 목적지는 내부 모델 서버 하나여야 함: %v (기대 %s)", got, want)
	}
	if len(sm.Mails()) != 1 {
		t.Fatalf("메일은 고객 SMTP 로 가야 함: %d", len(sm.Mails()))
	}
}

// 같은 코어에 클라우드 프로필을 주면 S3·메일 API·호스팅 LLM 으로 나간다(대비).
func TestCloudProfileUsesTheHostedAdapters(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	mk := func(h http.HandlerFunc) *httptest.Server { s := httptest.NewServer(h); t.Cleanup(s.Close); return s }
	s3 := mk(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mailAPI := mk(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	prof, _ := mock.Lookup("full")
	llmSrv := httptest.NewServer(&mock.Server{P: prof})
	t.Cleanup(llmSrv.Close)

	rec := &recorder{hosts: map[string]int{}, next: http.DefaultTransport}
	cfg := baseConfig(db)
	cfg.Profile = config.ProfileCloud
	cfg.Storage = config.Storage{Kind: "s3", S3: config.S3{Endpoint: s3.URL, Bucket: "b", Region: "r", AccessKey: "a", SecretKey: "s"}}
	cfg.Mail = config.Mail{Kind: "api", From: "x@y.test", API: config.MailAPI{URL: mailAPI.URL}}
	cfg.LLM = config.LLM{Kind: "hosted", BaseURL: llmSrv.URL, Model: "m"}
	e := start(t, cfg, "", "", &http.Client{Transport: rec})
	runScenario(t, e)
	if got := rec.list(); len(got) != 3 {
		t.Fatalf("클라우드 프로필은 세 곳(S3·메일 API·LLM)으로 나가야 함: %v", got)
	}
}

func TestOnpremProfileRefusesToBuildWithCloudAdapters(t *testing.T) {
	db := newDB(t)
	cfg := baseConfig(db)
	cfg.Storage = config.Storage{Kind: "s3", S3: config.S3{Endpoint: "https://s3.example", Bucket: "b"}}
	if _, _, err := wiring.Build(t.Context(), cfg, "", "", wiring.Options{}); err == nil || !strings.Contains(err.Error(), "s3") {
		t.Fatalf("설치형에서 S3 를 고르면 서버가 뜨지 않아야 함: %v", err)
	}
}
