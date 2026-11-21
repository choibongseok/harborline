package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTP 는 고객 SMTP 서버로 STARTTLS 를 열어 보낸다. 서버 인증서는 Roots(사설 CA 포함)로만 검증한다.
type SMTP struct {
	Host     string
	Port     int
	StartTLS bool
	Username string
	Password string
	Roots    *x509.CertPool
	Timeout  time.Duration
	// Dialer 는 시험에서 접속 경로를 바꿀 때만 쓴다.
	Dialer func(ctx context.Context, addr string) (net.Conn, error)
}

func (s *SMTP) Kind() string { return "smtp" }

func clean(v string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(v) }

func (s *SMTP) Send(ctx context.Context, m Message) error {
	to := time.Second * 15
	if s.Timeout > 0 {
		to = s.Timeout
	}
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	var conn net.Conn
	var err error
	if s.Dialer != nil {
		conn, err = s.Dialer(ctx, addr)
	} else {
		d := net.Dialer{Timeout: to}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp 연결: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(to * 2))
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp 인사: %w", err)
	}
	defer c.Close()
	if s.StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("서버가 STARTTLS 를 지원하지 않음(평문 전송 거부)")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.Host, RootCAs: s.Roots, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp STARTTLS(인증서 검증 포함): %w", err)
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("smtp 인증: %w", err)
		}
	}
	if err := c.Mail(clean(m.From)); err != nil {
		return err
	}
	if err := c.Rcpt(clean(m.To)); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + clean(m.From) + "\r\nTo: " + clean(m.To) + "\r\nSubject: " + clean(m.Subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + strings.ReplaceAll(m.Body, "\n", "\r\n") + "\r\n"
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
