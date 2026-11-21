package mail

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/choibongseok/harborline/internal/mocksmtp"
	"github.com/choibongseok/harborline/internal/pki"
)

func smtpServer(t *testing.T, withTLS bool, ca *pki.CA) (*mocksmtp.Server, string, int) {
	t.Helper()
	s := &mocksmtp.Server{RequireTLS: withTLS}
	if withTLS {
		cert, err := ca.Issue("smtp.corp.test")
		if err != nil {
			t.Fatal(err)
		}
		s.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	_, p, _ := net.SplitHostPort(s.Addr())
	port, _ := strconv.Atoi(p)
	return s, "127.0.0.1", port
}

func TestSMTPStartTLSWithPrivateCADeliversMail(t *testing.T) {
	ca, _ := pki.NewCA("corp-ca", time.Now().Add(time.Hour*24))
	srv, _, port := smtpServer(t, true, ca)
	// 서버 인증서의 이름(smtp.corp.test)으로 검증하도록 이름만 바꿔 연결한다.
	m := &SMTP{Host: "smtp.corp.test", Port: port, StartTLS: true, Roots: ca.Pool()}
	err := sendVia(m, "127.0.0.1", Message{From: "support@h.test", To: "c@x.test", Subject: "Re: 문의", Body: "답변입니다"})
	if err != nil {
		t.Fatal(err)
	}
	got := srv.Mails()
	if len(got) != 1 || !got[0].TLS || got[0].To != "c@x.test" || !strings.Contains(got[0].Data, "답변입니다") {
		t.Fatalf("TLS 로 한 통 전달되어야 함: %+v", got)
	}
}

// sendVia 는 SMTP.Host(인증서 이름)와 실제 접속 주소가 다른 시험 환경용이다.
func sendVia(m *SMTP, realHost string, msg Message) error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		up, err := net.Dial("tcp", net.JoinHostPort(realHost, strconv.Itoa(m.Port)))
		if err != nil {
			c.Close()
			return
		}
		go io.Copy(up, c)
		io.Copy(c, up)
		c.Close()
		up.Close()
	}()
	_, p, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(p)
	cp := *m
	cp.Port = port
	cp.Dialer = func(ctx context.Context, addr string) (net.Conn, error) {
		return net.Dial("tcp", net.JoinHostPort("127.0.0.1", p))
	}
	return cp.Send(context.Background(), msg)
}

func TestSMTPRejectsServerCertFromUntrustedCA(t *testing.T) {
	ca, _ := pki.NewCA("corp-ca", time.Now().Add(time.Hour*24))
	other, _ := pki.NewCA("other-ca", time.Now().Add(time.Hour*24))
	srv, _, port := smtpServer(t, true, ca)
	m := &SMTP{Host: "smtp.corp.test", Port: port, StartTLS: true, Roots: other.Pool()}
	err := sendVia(m, "127.0.0.1", Message{From: "a@b.test", To: "c@d.test", Subject: "s", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("믿지 않는 CA 의 인증서는 거부해야 함: %v", err)
	}
	if len(srv.Mails()) != 0 {
		t.Fatal("검증 실패 시 메일이 전달되면 안 됨")
	}
}

func TestSMTPRefusesPlaintextWhenStartTLSRequiredButUnsupported(t *testing.T) {
	srv, host, port := smtpServer(t, false, nil)
	m := &SMTP{Host: host, Port: port, StartTLS: true}
	err := m.Send(context.Background(), Message{From: "a@b.test", To: "c@d.test", Subject: "s", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "평문") {
		t.Fatalf("STARTTLS 미지원 서버에 평문으로 보내면 안 됨: %v", err)
	}
	if len(srv.Mails()) != 0 {
		t.Fatal("평문 전송이 일어남")
	}
}

func TestSMTPStripsHeaderInjection(t *testing.T) {
	srv, host, port := smtpServer(t, false, nil)
	m := &SMTP{Host: host, Port: port, StartTLS: false}
	err := m.Send(context.Background(), Message{From: "a@b.test", To: "c@d.test", Subject: "hi\r\nBcc: evil@x.test", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if d := srv.Mails()[0].Data; strings.Contains(d, "\r\nBcc:") {
		t.Fatalf("제목의 개행으로 헤더가 주입됨: %q", d)
	}
}

func TestAPIMailerPostsJSONWithToken(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(202)
	}))
	defer srv.Close()
	a := &API{URL: srv.URL, Token: "tok", Client: srv.Client()}
	if err := a.Send(context.Background(), Message{From: "a@b.test", To: "c@d.test", Subject: "s", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" || !strings.Contains(gotBody, `"to":"c@d.test"`) {
		t.Fatalf("%q %q", gotAuth, gotBody)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()
	if err := (&API{URL: bad.URL, Client: bad.Client()}).Send(context.Background(), Message{}); err == nil {
		t.Fatal("5xx 는 오류")
	}
}
