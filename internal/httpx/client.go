// Package httpx 는 나가는 HTTP(S) 호출의 공통 클라이언트를 만든다.
// 사내 프록시(HTTPS_PROXY/NO_PROXY)와 사설 CA 를 설정으로 받는다. 프로세스 전역 환경 변수에 기대지 않는다.
package httpx

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// Options 는 클라이언트 설정이다.
type Options struct {
	ProxyURL string // 예: http://proxy.corp:3128 (비면 직접 연결)
	NoProxy  string // 쉼표 구분 호스트·도메인·CIDR
	CAFile   string // 추가로 믿을 사설 CA(PEM). 비면 시스템 CA 만
	Timeout  time.Duration
	// Dial 은 테스트에서 이름 풀이를 바꿀 때만 쓴다.
	Dial func(network, addr string) (net.Conn, error)
}

// CertPool 은 시스템 CA 에 PEM 파일의 CA 를 더한 풀을 돌려준다.
func CertPool(caFile string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile == "" {
		return pool, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("CA 파일 읽기: %w", err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA 파일에 인증서가 없음: %s", caFile)
	}
	return pool, nil
}

// New 는 설정대로 프록시·CA 를 쓰는 http.Client 를 만든다.
func New(o Options) (*http.Client, error) {
	pool, err := CertPool(o.CAFile)
	if err != nil {
		return nil, err
	}
	cfg := httpproxy.Config{HTTPProxy: o.ProxyURL, HTTPSProxy: o.ProxyURL, NoProxy: o.NoProxy}
	if o.ProxyURL != "" {
		if _, err := url.Parse(o.ProxyURL); err != nil {
			return nil, fmt.Errorf("프록시 주소: %w", err)
		}
	}
	proxyFn := cfg.ProxyFunc()
	tr := &http.Transport{
		Proxy:               func(r *http.Request) (*url.URL, error) { return proxyFn(r.URL) },
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
	}
	if o.Dial != nil {
		tr.Dial = o.Dial
	}
	to := o.Timeout
	if to == 0 {
		to = 30 * time.Second
	}
	return &http.Client{Transport: tr, Timeout: to}, nil
}
