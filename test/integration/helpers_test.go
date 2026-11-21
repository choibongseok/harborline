//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choibongseok/harborline/internal/config"
	"github.com/choibongseok/harborline/internal/migrate"
	"github.com/choibongseok/harborline/internal/wiring"
)

const adminURL = "postgres://postgres:test@127.0.0.1:56433/postgres"

// newDB 는 테스트마다 새 데이터베이스를 만들고 접속 주소를 돌려준다.
func newDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("테스트 PostgreSQL(56433)에 닿지 않음: make infra 필요: %v", err)
	}
	defer c.Close(ctx)
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := "t_" + hex.EncodeToString(b)
	if _, err := c.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c2, err := pgx.Connect(ctx, adminURL)
		if err == nil {
			_, _ = c2.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c2.Close(ctx)
		}
	})
	return "postgres://postgres:test@127.0.0.1:56433/" + name
}

func repoPath(rel ...string) string {
	wd, _ := os.Getwd()
	return filepath.Join(append([]string{wd, "..", ".."}, rel...)...)
}

// migrateUpTo 는 마이그레이션 중 release 가 maxRelease 이하인 것만 적용한다.
func migrateFiles(t *testing.T, names ...string) []migrate.File {
	t.Helper()
	var fs []migrate.File
	for _, n := range names {
		b, err := os.ReadFile(repoPath("migrations", n))
		if err != nil {
			t.Fatal(err)
		}
		fs = append(fs, migrate.File{Name: n, SQL: string(b)})
	}
	return fs
}

func applyMigrations(t *testing.T, dbURL string, names ...string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := migrate.Apply(ctx, pool, migrateFiles(t, names...)); err != nil {
		t.Fatal(err)
	}
}

// baseConfig 는 설치형 프로필 기본 설정이다(SMTP·LLM 은 호출하는 쪽에서 채운다).
func baseConfig(dbURL string) config.Config {
	return config.Config{
		Profile: config.ProfileOnprem, DatabaseURL: dbURL, SessionSecret: "0123456789abcdef-secret", Version: "v-test", AdminPassword: "admin-pw",
		Storage: config.Storage{Kind: "disk", Dir: ""},
		Mail:    config.Mail{Kind: "smtp", From: "support@harborline.test", SMTP: config.SMTP{Host: "localhost", Port: 25}},
		Auth:    config.Auth{Kind: "local"},
		LLM:     config.LLM{Kind: "off"},
	}
}

type env struct {
	URL    string
	Client *http.Client
}

// start 는 wiring 으로 서버를 만들고 httptest 로 띄운다.
func start(t *testing.T, cfg config.Config, features, brk string, hc *http.Client) *env {
	t.Helper()
	if cfg.Storage.Dir == "" {
		cfg.Storage.Dir = t.TempDir()
	}
	ts := httptest.NewUnstartedServer(nil)
	if cfg.Auth.Kind == "oidc" && cfg.Auth.OIDC.RedirectURL == "" {
		cfg.Auth.OIDC.RedirectURL = "http://" + ts.Listener.Addr().String() + "/auth/callback"
	}
	srv, closeFn, err := wiring.Build(context.Background(), cfg, features, brk, wiring.Options{HTTP: hc})
	if err != nil {
		ts.Listener.Close()
		t.Fatal(err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(func() { ts.Close(); closeFn() })
	jar, _ := cookiejar.New(nil)
	return &env{URL: ts.URL, Client: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

func (e *env) do(t *testing.T, method, path string, body any) (int, map[string]any, []byte) {
	t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m, raw
}

func (e *env) login(t *testing.T, user, pw string) int {
	code, _, _ := e.do(t, "POST", "/auth/login", map[string]string{"username": user, "password": pw})
	return code
}

func (e *env) createInquiry(t *testing.T, subject, body string) string {
	code, m, raw := e.do(t, "POST", "/api/inquiries", map[string]string{"subject": subject, "body": body, "email": "customer@example.test"})
	if code != 201 {
		t.Fatalf("문의 생성 %d: %s", code, raw)
	}
	return m["id"].(string)
}

func portOf(addr string) int {
	i := strings.LastIndex(addr, ":")
	p, _ := strconv.Atoi(addr[i+1:])
	return p
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var _ = fmt.Sprintf
