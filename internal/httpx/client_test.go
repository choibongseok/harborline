package httpx

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choibongseok/harborline/internal/mitmproxy"
	"github.com/choibongseok/harborline/internal/pki"
)

// 외부 API 대역(평문 HTTP). 검사형 프록시가 이쪽으로 넘긴다.
func setup(t *testing.T) (*mitmproxy.Proxy, string, string) {
	t.Helper()
	ca, err := pki.NewCA("test-corp-ca", time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "external:"+r.Host) }))
	t.Cleanup(ext.Close)
	px := &mitmproxy.Proxy{CA: ca, Upstream: strings.TrimPrefix(ext.URL, "http://")}
	ps := httptest.NewServer(px)
	t.Cleanup(ps.Close)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, ca.PEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return px, ps.URL, caFile
}

func get(c *http.Client, u string) (string, error) {
	resp, err := c.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestProxyWithPrivateCAReachesExternalHost(t *testing.T) {
	px, proxyURL, caFile := setup(t)
	c, err := New(Options{ProxyURL: proxyURL, CAFile: caFile})
	if err != nil {
		t.Fatal(err)
	}
	body, err := get(c, "https://mail-api.example.test/send")
	if err != nil || body != "external:mail-api.example.test" {
		t.Fatalf("프록시와 사설 CA 로 외부 호출이 되어야 함: %q %v", body, err)
	}
	if h := mitmproxy.Hosts(px.Log()); len(h) != 1 || h[0] != "mail-api.example.test:443" {
		t.Fatalf("프록시에 CONNECT 가 기록되어야 함: %v", h)
	}
}

func TestProxyWithoutPrivateCAFailsCertificateVerification(t *testing.T) {
	_, proxyURL, _ := setup(t)
	c, _ := New(Options{ProxyURL: proxyURL})
	_, err := get(c, "https://mail-api.example.test/send")
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("사설 CA 를 모르면 인증서 검증에서 실패해야 함: %v", err)
	}
}

func TestNoProxyHostsBypassTheProxy(t *testing.T) {
	px, proxyURL, caFile := setup(t)
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "internal:"+r.Host) }))
	defer internal.Close()
	c, _ := New(Options{ProxyURL: proxyURL, CAFile: caFile, NoProxy: "llm.corp.test,.internal.test",
		Dial: func(network, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "llm.corp.test") {
				addr = strings.TrimPrefix(internal.URL, "http://")
			}
			return net.Dial(network, addr)
		}})
	body, err := get(c, "http://llm.corp.test/v1/models")
	if err != nil || !strings.HasPrefix(body, "internal:") {
		t.Fatalf("NO_PROXY 대상은 직접 연결되어야 함: %q %v", body, err)
	}
	if n := len(px.Log()); n != 0 {
		t.Fatalf("NO_PROXY 호출이 프록시에 보이면 안 됨: %v", px.Log())
	}
}

func TestNewRejectsBadCAFile(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "x.pem")
	_ = os.WriteFile(bad, []byte("not pem"), 0o600)
	if _, err := New(Options{CAFile: bad}); err == nil {
		t.Fatal("PEM 이 아닌 CA 파일은 거부해야 함")
	}
	if _, err := New(Options{CAFile: filepath.Join(t.TempDir(), "none.pem")}); err == nil {
		t.Fatal("없는 CA 파일은 거부해야 함")
	}
}
