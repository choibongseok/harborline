//go:build e2e

// 격리 네트워크(외부로 나가는 길 없음)에서 설치 → 업그레이드 → 실패 주입 → 롤백 → 백업·복구를 harborctl 로 끝까지 돌린다.
package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	root     string // 저장소 루트
	certsDir string
	instDir  string
	pubKey   string
	client   *http.Client
)

const (
	gateway = "http://127.0.0.1:58180"
	project = "hl-e2e"
)

func sh(dir string, env []string, name string, args ...string) (string, int) {
	c := exec.Command(name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	code := 0
	if err != nil {
		code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return out.String(), code
}

func TestMain(m *testing.M) {
	wd, _ := os.Getwd()
	root = filepath.Join(wd, "..", "..")
	certsDir = filepath.Join(root, "dist", "e2e-certs")
	instDir = filepath.Join(root, "dist", "e2e-install")
	pubKey = filepath.Join(root, "dist", "keys", "release.pub")
	if err := setup(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e 준비 실패:", err)
		os.Exit(1)
	}
	code := m.Run()
	if os.Getenv("HL_KEEP_SITE") == "" {
		sh(root, siteEnv(), "docker", "compose", "-f", "test/e2e/site.compose.yml", "down", "-v", "--remove-orphans")
	}
	os.Exit(code)
}

func siteEnv() []string { return []string{"HL_CERTS=" + certsDir} }

func setup() error {
	steps := [][]string{
		{"go", "build", "-o", "dist/bin/harborctl", "./cmd/harborctl"},
		{"go", "run", "./cmd/hltools", "gencerts", "--cert", certsDir},
	}
	for _, s := range steps {
		if out, code := sh(root, nil, s[0], s[1:]...); code != 0 {
			return fmt.Errorf("%v: %s", s, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "dist", "bundles", "harborline-v2.0.0", "manifest.json")); err != nil {
		return fmt.Errorf("번들이 없음: make bundle 을 먼저 실행하세요")
	}
	sh(root, nil, "go", "build", "-o", "dist/build/tools/hltools-host", "./cmd/hltools")
	if out, code := sh(root, siteEnv(), "docker", "compose", "-f", "test/e2e/site.compose.yml", "up", "-d", "--remove-orphans"); code != 0 {
		return fmt.Errorf("사이트 모의 기동: %s", out)
	}
	jar, _ := cookiejar.New(nil)
	client = &http.Client{Jar: jar, Timeout: 20 * time.Second}
	return nil
}

// ---- harborctl ----

func configJSON(extra func(map[string]any)) map[string]any {
	c := map[string]any{
		"project":            project,
		"network":            map[string]any{"name": "hl-e2e-site", "external": true},
		"dns":                []string{"10.77.0.2"},
		"health_timeout_sec": 20,
		"min_disk_gb":        1,
		"ca_files":           []string{filepath.Join(certsDir, "ca.pem")},
		"mounts":             []map[string]string{{"host": filepath.Join(certsDir, "ca.pem"), "container": "/etc/harborline/ca.pem"}},
		"tls_checks":         []map[string]string{{"addr": "smtp.corp.test:587", "starttls": "smtp", "ca_file": "/etc/harborline/ca.pem"}},
		"env": map[string]string{
			"HARBORLINE_SMTP_HOST": "smtp.corp.test", "HARBORLINE_SMTP_PORT": "587", "HARBORLINE_CA_FILE": "/etc/harborline/ca.pem",
			"HARBORLINE_LLM_BASE_URL": "http://llm.corp.test:8000", "HARBORLINE_ADMIN_PASSWORD": "admin-pw",
		},
	}
	if extra != nil {
		extra(c)
	}
	return c
}

func writeConfig(t *testing.T, c map[string]any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "install.json")
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func bundle(v string) string { return filepath.Join(root, "dist", "bundles", "harborline-"+v) }

// ctl 은 harborctl 을 실행한다. 출력과 종료 코드를 돌려준다.
func ctl(t *testing.T, args ...string) (string, int) {
	t.Helper()
	full := append(args, "--dir", instDir)
	out, code := sh(root, nil, filepath.Join(root, "dist", "bin", "harborctl"), full...)
	t.Logf("$ harborctl %s\n%s(종료 코드 %d)", strings.Join(args, " "), out, code)
	return out, code
}

func destroy() {
	sh(root, nil, filepath.Join(root, "dist", "bin", "harborctl"), "destroy", "--dir", instDir)
	sh(root, nil, "docker", "compose", "-p", project, "down", "-v", "--remove-orphans")
	_ = os.RemoveAll(instDir)
}

// fresh 는 깨끗한 설치 폴더에서 version 을 설치하고 테스트가 끝나면 지운다.
func fresh(t *testing.T, version string, extra func(map[string]any)) {
	t.Helper()
	destroy()
	t.Cleanup(destroy)
	cfg := writeConfig(t, configJSON(extra))
	out, code := ctl(t, "install", "--bundle", bundle(version), "--config", cfg, "--pubkey", pubKey)
	if code != 0 {
		t.Fatalf("설치 실패(%d): %s", code, out)
	}
}

func upgrade(t *testing.T, version string) (string, int) {
	return ctl(t, "upgrade", "--bundle", bundle(version), "--pubkey", pubKey)
}

type state struct {
	Version, Schema, Previous, Status string
	LastBackup                        string `json:"last_backup"`
}

func readState(t *testing.T) state {
	t.Helper()
	var s state
	b, err := os.ReadFile(filepath.Join(instDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &s)
	return s
}

// ---- 앱 API (gateway 경유) ----

func api(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, gateway+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func login(t *testing.T) {
	t.Helper()
	if code, m := api(t, "POST", "/auth/login", map[string]string{"username": "admin", "password": "admin-pw"}); code != 200 {
		t.Fatalf("로그인 %d %v", code, m)
	}
}

func listItems(t *testing.T) []any {
	t.Helper()
	req, _ := http.NewRequest("GET", gateway+"/api/inquiries?limit=200", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// seed 는 문의 n 건(각 첨부 하나)을 만들고 첫 건에 답변한다.
func seed(t *testing.T, n int) {
	t.Helper()
	login(t)
	for i := 0; i < n; i++ {
		code, m := api(t, "POST", "/api/inquiries", map[string]string{"subject": fmt.Sprintf("문의 %d", i), "body": fmt.Sprintf("본문 %d refund", i), "email": "customer@example.test"})
		if code != 201 {
			t.Fatalf("문의 생성 %d", code)
		}
		id := m["id"].(string)
		if c, _ := api(t, "POST", "/api/inquiries/"+id+"/attachments?name=log.txt", strings.Repeat(fmt.Sprintf("line %d\n", i), 200)); c != 201 {
			t.Fatalf("첨부 %d", c)
		}
		if i == 0 {
			if c, m := api(t, "POST", "/api/inquiries/"+id+"/replies", map[string]string{"body": "확인했습니다"}); c != 201 || m["mailed"] != true {
				t.Fatalf("답변 %d %v", c, m)
			}
		}
	}
}

// snapshot 은 DB 의 문의 건수·해시와 첨부 파일들의 해시를 한 줄로 요약한다(복구·롤백 뒤 같은 값이어야 한다).
func snapshot(t *testing.T) string {
	t.Helper()
	out, code := sh(root, nil, "docker", "exec", project+"-db", "psql", "-U", "harborline", "-d", "harborline", "-tA", "-c",
		"SELECT count(*) || ' ' || coalesce(md5(string_agg(id::text||subject||body, ',' ORDER BY id)),'-') FROM inquiries")
	if code != 0 {
		t.Fatalf("스냅샷 쿼리: %s", out)
	}
	h := sha256.New()
	n := 0
	_ = filepath.WalkDir(filepath.Join(instDir, "data", "attachments"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(instDir, p)
			h.Write([]byte(rel))
			h.Write(b)
			n++
		}
		return nil
	})
	return fmt.Sprintf("db[%s] files[%d %s]", strings.TrimSpace(out), n, hex.EncodeToString(h.Sum(nil))[:12])
}

func psql(t *testing.T, sql string) string {
	t.Helper()
	out, code := sh(root, nil, "docker", "exec", project+"-db", "psql", "-U", "harborline", "-d", "harborline", "-tA", "-c", sql)
	if code != 0 {
		t.Fatalf("psql: %s", out)
	}
	return strings.TrimSpace(out)
}

// ---- 외부 접속 시도 기록(sinkhole) ----

const controlName = "control.invalid"

// sinkLines 는 싱크홀에 남은 기록에서 시험용 대조군(positive control) 줄을 뺀 것이다.
func sinkLines(t *testing.T) []string {
	t.Helper()
	out, _ := sh(root, nil, "docker", "logs", "hl-e2e-site-sinkhole-1")
	var ls []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "SINK ") && !strings.Contains(l, controlName) {
			ls = append(ls, l)
		}
	}
	return ls
}

func assertNoExternalAttempts(t *testing.T) {
	t.Helper()
	if ls := sinkLines(t); len(ls) != 0 {
		t.Fatalf("외부로 나가려는 시도가 기록됨(싱크홀 %d건):\n%s", len(ls), strings.Join(ls, "\n"))
	}
}

func mailsReceived(t *testing.T) []string {
	out, _ := sh(root, nil, "docker", "logs", "hl-e2e-site-smtp-1")
	var ls []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "MAIL ") {
			ls = append(ls, l)
		}
	}
	return ls
}
