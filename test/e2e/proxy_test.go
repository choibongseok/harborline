//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choibongseok/harborline/internal/pki"
)

// proxyUp 는 사내 프록시 + 외부 서비스 모의를 띄우고, 프록시 CA 파일 경로를 돌려준다.
func proxyUp(t *testing.T) string {
	t.Helper()
	ca, err := pki.NewCA("corp-proxy-ca", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "proxy-ca.pem"), ca.PEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proxy-ca.key"), ca.KeyPEM(), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(siteEnv(), "HL_PROXY_CA="+dir)
	down := func() {
		sh(root, env, "docker", "compose", "-f", "test/e2e/proxy.compose.yml", "down", "-v", "--remove-orphans")
	}
	down()
	if out, code := sh(root, env, "docker", "compose", "-f", "test/e2e/proxy.compose.yml", "up", "-d"); code != 0 {
		t.Fatalf("프록시 기동: %s", out)
	}
	t.Cleanup(down)
	time.Sleep(time.Second)
	return filepath.Join(dir, "proxy-ca.pem")
}

func logLines(container, prefix string) []string {
	out, _ := sh(root, nil, "docker", "logs", container)
	var ls []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			ls = append(ls, l)
		}
	}
	return ls
}

func proxyInstall(t *testing.T, caHost string) {
	fresh(t, "v1.0.0", func(c map[string]any) {
		env := c["env"].(map[string]string)
		env["HARBORLINE_PROFILE"] = "cloud" // 클라우드 어댑터 일부(메일 API)를 쓰는 환경: 아웃바운드가 프록시로만 열려 있다
		env["HARBORLINE_STORAGE"] = "disk"
		env["HARBORLINE_MAIL"] = "api"
		env["HARBORLINE_MAIL_API_URL"] = "https://mail-api.example.test/send"
		env["HARBORLINE_HTTPS_PROXY"] = "http://proxy.corp.test:3128"
		env["HARBORLINE_NO_PROXY"] = "llm.corp.test,smtp.corp.test"
		if caHost != "" {
			env["HARBORLINE_CA_FILE"] = "/etc/harborline/proxy-ca.pem"
			c["mounts"] = []map[string]string{{"host": caHost, "container": "/etc/harborline/proxy-ca.pem"}}
			c["ca_files"] = []string{caHost}
		} else {
			delete(env, "HARBORLINE_CA_FILE")
			c["mounts"] = []map[string]string{}
			c["tls_checks"] = []map[string]string{}
		}
	})
}

func TestOnlyExternalCallsGoThroughProxyAndInternalCallsStayDirect(t *testing.T) {
	ca := proxyUp(t)
	proxyInstall(t, ca)
	seed(t, 1) // 답변 → 메일 API(외부, 프록시 경유). seed 가 mailed=true 를 확인한다.
	id := listItems(t)[0].(map[string]any)["id"].(string)
	if c, m := api(t, "POST", "/api/inquiries/"+id+"/draft", nil); c != 200 { // 내부 모델 서버(NO_PROXY, 직접)
		t.Fatalf("내부 모델 서버 호출: %d %v", c, m)
	}
	px := logLines("hl-e2e-proxy-proxy-1", "PROXY ")
	joined := strings.Join(px, "\n")
	if !strings.Contains(joined, "CONNECT host=mail-api.example.test:443") {
		t.Fatalf("메일 API 호출은 프록시를 타야 함:\n%s", joined)
	}
	for _, internal := range []string{"llm.corp.test", "smtp.corp.test", "db"} {
		if strings.Contains(joined, internal) {
			t.Fatalf("내부 호출(%s)이 프록시를 타면 안 됨:\n%s", internal, joined)
		}
	}
	if ext := logLines("hl-e2e-proxy-ext-1", "EXT mail-api"); len(ext) != 1 {
		t.Fatalf("외부 메일 API 에는 정확히 한 번 도착해야 함: %v", ext)
	}
	assertNoExternalAttempts(t) // 프록시를 우회한 직접 접속 시도는 없다
}

func TestProxyTLSInterceptionFailsClosedWithoutThePrivateCA(t *testing.T) {
	proxyUp(t)
	proxyInstall(t, "") // 사설 CA 를 앱에 주지 않음
	login(t)
	code, m := api(t, "POST", "/api/inquiries", map[string]string{"subject": "s", "body": "b", "email": "c@example.test"})
	if code != 201 {
		t.Fatal("문의 생성")
	}
	id := m["id"].(string)
	_, rep := api(t, "POST", "/api/inquiries/"+id+"/replies", map[string]string{"body": "답변"})
	if rep["mailed"] != false {
		t.Fatalf("사설 CA 를 모르면 프록시가 연 TLS 를 거부해 메일이 나가면 안 됨: %v", rep)
	}
	if px := strings.Join(logLines("hl-e2e-proxy-proxy-1", "PROXY "), "\n"); !strings.Contains(px, "CONNECT host=mail-api.example.test:443") {
		t.Fatalf("시도는 프록시까지 도달해야 함:\n%s", px)
	}
	if ext := logLines("hl-e2e-proxy-ext-1", "EXT mail-api"); len(ext) != 0 {
		t.Fatalf("검증 실패 시 외부 서비스에 요청이 도달하면 안 됨: %v", ext)
	}
	assertNoExternalAttempts(t)
}
