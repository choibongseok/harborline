// Package mitmproxy 는 사내 HTTP 프록시 모의다. CONNECT 를 받아 사설 CA 로 TLS 를 열고(검사형 프록시),
// 요청을 지정한 상위 서버로 넘기며 모든 접속을 기록한다.
package mitmproxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/choibongseok/harborline/internal/pki"
)

// Entry 는 프록시를 거친 요청 한 건이다.
type Entry struct {
	Kind string // CONNECT | HTTP
	Host string
	Path string
}

// Proxy 는 검사형 CONNECT 프록시다.
type Proxy struct {
	CA *pki.CA
	// Upstream 은 가로챈 요청을 보낼 http 주소(host:port). 비면 원래 목적지로 직접 보낸다.
	Upstream string
	// OnEntry 는 기록할 때마다 호출된다(선택).
	OnEntry func(Entry)
	mu      sync.Mutex
	log     []Entry
	certs   map[string]*tls.Certificate
}

func (p *Proxy) record(e Entry) {
	p.mu.Lock()
	p.log = append(p.log, e)
	p.mu.Unlock()
	if p.OnEntry != nil {
		p.OnEntry(e)
	}
}

// Log 는 기록 사본이다.
func (p *Proxy) Log() []Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Entry(nil), p.log...)
}

func (p *Proxy) cert(host string) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.certs == nil {
		p.certs = map[string]*tls.Certificate{}
	}
	if c, ok := p.certs[host]; ok {
		return c, nil
	}
	c, err := p.CA.Issue(host)
	if err != nil {
		return nil, err
	}
	p.certs[host] = &c
	return &c, nil
}

// ServeHTTP 는 프록시 요청을 처리한다.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		p.record(Entry{"HTTP", r.Host, r.URL.Path})
		p.forward(w, r, "http")
		return
	}
	host, _, _ := net.SplitHostPort(r.Host)
	p.record(Entry{"CONNECT", r.Host, ""})
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack 불가", 500)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	tc := tls.Server(conn, &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		name := h.ServerName
		if name == "" {
			name = host
		}
		return p.cert(name)
	}})
	ln := &oneConn{c: tc, done: make(chan struct{})}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, rr *http.Request) {
		rr.URL.Host = r.Host
		p.record(Entry{"HTTP", r.Host, rr.URL.Path})
		p.forward(w, rr, "https")
	})}
	_ = srv.Serve(ln)
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, scheme string) {
	target := &url.URL{Scheme: "http", Host: p.Upstream}
	if p.Upstream == "" {
		target = &url.URL{Scheme: scheme, Host: r.Host}
		if r.URL.Host != "" {
			target.Host = r.URL.Host
		}
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Director = func(out *http.Request) {
		out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
		out.Header.Set("X-Forwarded-Host", r.Host)
		out.Host = r.Host
	}
	rp.ServeHTTP(w, r)
}

// oneConn 은 이미 맺어진 연결 하나만 내주는 리스너다(연결이 닫히면 Serve 가 끝난다).
type oneConn struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
}

type closeNotify struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *closeNotify) Close() error { c.once.Do(func() { close(c.done) }); return c.Conn.Close() }

func (l *oneConn) Accept() (net.Conn, error) {
	var out net.Conn
	l.once.Do(func() { out = &closeNotify{Conn: l.c, done: l.done} })
	if out != nil {
		return out, nil
	}
	<-l.done
	return nil, fmt.Errorf("closed")
}
func (l *oneConn) Close() error   { return nil }
func (l *oneConn) Addr() net.Addr { return &net.TCPAddr{} }

// Hosts 는 기록에서 CONNECT 대상 호스트만 모은다.
func Hosts(es []Entry) []string {
	var out []string
	for _, e := range es {
		if e.Kind == "CONNECT" {
			out = append(out, strings.ToLower(e.Host))
		}
	}
	return out
}
