// Package mocksmtp 는 STARTTLS 를 지원하는 최소 SMTP 수신 서버다(고객 SMTP 모의).
package mocksmtp

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
)

// Mail 은 받은 메일 한 통이다.
type Mail struct {
	From, To string
	Data     string
	TLS      bool
}

// Server 는 받은 메일을 모아 둔다.
type Server struct {
	TLS         *tls.Config // nil 이면 STARTTLS 를 광고하지 않는다
	RequireTLS  bool        // true 면 STARTTLS 전에 MAIL 을 받지 않는다
	mu          sync.Mutex
	mails       []Mail
	ln          net.Listener
	Connections int
}

// Listen 은 addr 에서 받기 시작한다.
func (s *Server) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.Connections++
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	return nil
}

// Addr 는 실제 수신 주소다.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close 는 수신을 멈춘다.
func (s *Server) Close() { _ = s.ln.Close() }

// Mails 는 받은 메일 사본이다.
func (s *Server) Mails() []Mail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Mail(nil), s.mails...)
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	tlsOn := false
	r := bufio.NewReader(c)
	w := func(f string, a ...any) { fmt.Fprintf(c, f+"\r\n", a...) }
	w("220 mocksmtp ready")
	var cur Mail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if s.TLS != nil && !tlsOn {
				fmt.Fprint(c, "250-mocksmtp\r\n250 STARTTLS\r\n")
			} else {
				w("250 mocksmtp")
			}
		case cmd == "STARTTLS" && s.TLS != nil && !tlsOn:
			w("220 go ahead")
			t := tls.Server(c, s.TLS)
			if err := t.Handshake(); err != nil {
				return
			}
			c, tlsOn, r = t, true, bufio.NewReader(t)
			w = func(f string, a ...any) { fmt.Fprintf(t, f+"\r\n", a...) }
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if s.RequireTLS && !tlsOn {
				w("530 Must issue a STARTTLS command first")
				continue
			}
			cur = Mail{From: strings.Trim(strings.TrimSpace(line[10:]), "<>"), TLS: tlsOn}
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			cur.To = strings.Trim(strings.TrimSpace(line[8:]), "<>")
			w("250 ok")
		case cmd == "DATA":
			w("354 end with .")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			cur.Data = b.String()
			s.mu.Lock()
			s.mails = append(s.mails, cur)
			s.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}
