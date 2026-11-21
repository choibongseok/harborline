// hltools 는 시험·시연용 보조 서버 모음이다(고객 환경 모의): mockllm, mocksmtp, sinkhole, forward, proxy, ext.
package main

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/choibongseok/harborline/internal/llm/mock"
	"github.com/choibongseok/harborline/internal/mitmproxy"
	"github.com/choibongseok/harborline/internal/mocksmtp"
	"github.com/choibongseok/harborline/internal/pki"
)

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "hltools:", err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		die(fmt.Errorf("사용: hltools mockllm|mocksmtp|sinkhole|forward|proxy|ext"))
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	listen := fs.String("listen", ":8080", "수신 주소")
	profile := fs.String("profile", "full", "mockllm 프로필")
	cert := fs.String("cert", "", "TLS 인증서 PEM")
	key := fs.String("key", "", "TLS 개인키 PEM")
	requireTLS := fs.Bool("require-tls", false, "STARTTLS 전에는 메일을 받지 않음")
	ip := fs.String("ip", "", "sinkhole 이 DNS 응답으로 돌려줄 자기 IP")
	to := fs.String("to", "", "forward 대상 host:port")
	caCert := fs.String("ca-cert", "", "프록시 CA 인증서")
	caKey := fs.String("ca-key", "", "프록시 CA 개인키")
	upstream := fs.String("upstream", "", "프록시가 요청을 넘길 host:port")
	_ = fs.Parse(os.Args[2:])
	switch os.Args[1] {
	case "mockllm":
		p, ok := mock.Lookup(*profile)
		if !ok {
			die(fmt.Errorf("프로필 없음: %s", *profile))
		}
		fmt.Println("mockllm", p.Name, "tools:", p.Tools, "json:", p.JSONMode, "ctx:", p.Ctx)
		die(http.ListenAndServe(*listen, &mock.Server{P: p}))
	case "mocksmtp":
		s := &mocksmtp.Server{RequireTLS: *requireTLS, OnMail: func(m mocksmtp.Mail) {
			fmt.Printf("MAIL to=%s from=%s tls=%v\n", m.To, m.From, m.TLS)
		}}
		if *cert != "" {
			c, err := tls.LoadX509KeyPair(*cert, *key)
			die(err)
			s.TLS = &tls.Config{Certificates: []tls.Certificate{c}}
		}
		die(s.Listen(*listen))
		fmt.Println("mocksmtp", *listen)
		select {}
	case "sinkhole":
		die(sinkhole(*ip))
	case "forward":
		die(forward(*listen, *to))
	case "proxy":
		cp, err := os.ReadFile(*caCert)
		die(err)
		kp, err := os.ReadFile(*caKey)
		die(err)
		ca, err := pki.LoadCA(cp, kp)
		die(err)
		px := &mitmproxy.Proxy{CA: ca, Upstream: *upstream, OnEntry: func(e mitmproxy.Entry) {
			fmt.Printf("PROXY %s host=%s path=%s\n", e.Kind, e.Host, e.Path)
		}}
		fmt.Println("proxy", *listen, "upstream", *upstream)
		die(http.ListenAndServe(*listen, px))
	case "ext":
		die(ext(*listen))
	case "gencerts":
		die(gencerts(*cert))
	default:
		die(fmt.Errorf("알 수 없는 명령 %s", os.Args[1]))
	}
}

// gencerts 는 --cert 로 받은 폴더에 시연용 사설 CA 와 SMTP 서버 인증서(smtp.corp.test), 만료된 CA 를 만든다.
func gencerts(dir string) error {
	if dir == "" {
		return fmt.Errorf("--cert 에 출력 폴더를 주세요")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ca, err := pki.NewCA("harborline-demo-ca", time.Now().Add(365*24*time.Hour))
	if err != nil {
		return err
	}
	srv, err := ca.Issue("smtp.corp.test")
	if err != nil {
		return err
	}
	kb, _ := x509.MarshalECPrivateKey(srv.PrivateKey.(*ecdsa.PrivateKey))
	old, err := pki.NewCA("expired-demo-ca", time.Now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"ca.pem": ca.PEM, "ca.key": ca.KeyPEM(), "expired-ca.pem": old.PEM,
		"smtp.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate[0]}),
		"smtp.key": pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}),
	}
	for n, b := range files {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ext 는 "인터넷 쪽" 외부 서비스 모의다: 메일 API(POST /send)와 호스팅 LLM(/v1/*).
func ext(listen string) error {
	p, _ := mock.Lookup("full")
	llm := &mock.Server{P: p}
	mux := http.NewServeMux()
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		fmt.Printf("EXT mail-api host=%s bytes=%d\n", r.Host, len(b))
		w.WriteHeader(http.StatusAccepted)
	})
	mux.Handle("/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("EXT llm host=%s path=%s\n", r.Host, r.URL.Path)
		llm.ServeHTTP(w, r)
	}))
	return http.ListenAndServe(listen, mux)
}

// forward 는 TCP 포워더다(고객 쪽 리버스 프록시·로드밸런서 모의: 호스트에서 격리 네트워크 안의 앱으로 들어오는 유일한 길).
func forward(listen, to string) error {
	l, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	fmt.Println("forward", listen, "->", to)
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			up, err := net.DialTimeout("tcp", to, 5*time.Second)
			if err != nil {
				return
			}
			defer up.Close()
			go io.Copy(up, c)
			io.Copy(c, up)
		}()
	}
}

// sinkhole 은 DNS(53, UDP·TCP)와 흔한 포트의 TCP 접속을 받아 모두 기록한다. 이름 풀이는 항상 자기 IP 로 답한다.
// 외부로 나가려는 시도가 있으면 "SINK ..." 줄이 남는다.
func sinkhole(ip string) error {
	self := net.ParseIP(ip).To4()
	if self == nil {
		return fmt.Errorf("--ip 가 필요함")
	}
	var mu sync.Mutex
	logf := func(f string, a ...any) { mu.Lock(); fmt.Printf("SINK "+f+"\n", a...); mu.Unlock() }
	answer := func(req []byte, src string) []byte {
		var p dnsmessage.Parser
		h, err := p.Start(req)
		if err != nil {
			return nil
		}
		q, err := p.Question()
		if err != nil {
			return nil
		}
		logf("dns name=%s type=%v src=%s", strings.TrimSuffix(q.Name.String(), "."), q.Type, src)
		msg := dnsmessage.Message{Header: dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: []dnsmessage.Question{q}}
		if q.Type == dnsmessage.TypeA {
			var a [4]byte
			copy(a[:], self)
			msg.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 5}, Body: &dnsmessage.AResource{A: a}}}
		}
		b, _ := msg.Pack()
		return b
	}
	pc, err := net.ListenPacket("udp", ":53")
	if err != nil {
		return err
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if r := answer(buf[:n], addr.String()); r != nil {
				_, _ = pc.WriteTo(r, addr)
			}
		}
	}()
	tl, err := net.Listen("tcp", ":53")
	if err == nil {
		go func() {
			for {
				c, err := tl.Accept()
				if err != nil {
					return
				}
				go func() {
					defer c.Close()
					var lb [2]byte
					if _, err := io.ReadFull(c, lb[:]); err != nil {
						return
					}
					req := make([]byte, int(lb[0])<<8|int(lb[1]))
					if _, err := io.ReadFull(c, req); err != nil {
						return
					}
					if r := answer(req, c.RemoteAddr().String()); r != nil {
						_, _ = c.Write(append([]byte{byte(len(r) >> 8), byte(len(r))}, r...))
					}
				}()
			}
		}()
	}
	for _, port := range []string{"25", "80", "443", "465", "587", "993", "8080", "8443", "389", "636"} {
		l, err := net.Listen("tcp", ":"+port)
		if err != nil {
			continue
		}
		go func(port string) {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				logf("tcp port=%s src=%s", port, c.RemoteAddr())
				c.Close()
			}
		}(port)
	}
	fmt.Println("sinkhole", ip)
	select {}
}
