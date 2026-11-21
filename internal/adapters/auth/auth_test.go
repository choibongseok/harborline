package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestSessionRoundTripAndTamper(t *testing.T) {
	s := &Sessions{Secret: []byte("0123456789abcdef")}
	v := s.Issue(&Principal{Subject: "u1", Name: "홍길동", Roles: []string{RoleAgent}})
	p, err := s.Parse(v)
	if err != nil || p.Subject != "u1" || p.Name != "홍길동" {
		t.Fatalf("%+v %v", p, err)
	}
	payload, sig, _ := strings.Cut(v, ".")
	if _, err := s.Parse(payload + "." + sig[:len(sig)-2] + "AA"); err == nil {
		t.Fatal("서명이 바뀐 세션은 거부해야 함")
	}
	other := &Sessions{Secret: []byte("another-secret-xx")}
	if _, err := other.Parse(v); err == nil {
		t.Fatal("다른 키로는 열 수 없어야 함")
	}
}

func TestSessionExpires(t *testing.T) {
	now := time.Now()
	s := &Sessions{Secret: []byte("0123456789abcdef"), TTL: time.Minute, Now: func() time.Time { return now }}
	v := s.Issue(&Principal{Subject: "u"})
	now = now.Add(2 * time.Minute)
	if _, err := s.Parse(v); err == nil {
		t.Fatal("만료된 세션은 거부해야 함")
	}
}

func TestMapRolesFromGroups(t *testing.T) {
	rm := map[string]string{"support": RoleAgent, "admins": RoleAdmin}
	roles, err := MapRoles([]string{"dev", "support", "admins", "support"}, rm)
	if err != nil || len(roles) != 2 {
		t.Fatalf("%v %v", roles, err)
	}
	if _, err := MapRoles([]string{"dev", "qa"}, rm); !errors.Is(err, ErrNoRole) {
		t.Fatalf("매핑된 그룹이 없으면 ErrNoRole: %v", err)
	}
}

func TestAdminImpliesAgent(t *testing.T) {
	if !(&Principal{Roles: []string{RoleAdmin}}).HasRole(RoleAgent) {
		t.Fatal("admin 은 agent 권한을 포함해야 함")
	}
	if (&Principal{Roles: []string{RoleAgent}}).HasRole(RoleAdmin) {
		t.Fatal("agent 는 admin 이 아님")
	}
}

type fakeUsers map[string][2]string

func (f fakeUsers) UserHash(_ context.Context, u string) (string, string, error) {
	v, ok := f[u]
	if !ok {
		return "", "", errors.New("none")
	}
	return v[0], v[1], nil
}

func TestLocalLoginChecksBcryptHash(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("pw-1234"), bcrypt.MinCost)
	l := &Local{Users: fakeUsers{"kim": {string(h), RoleAgent}}, Sessions: &Sessions{Secret: []byte("0123456789abcdef")}}
	p, err := l.Login(context.Background(), "kim", "pw-1234")
	if err != nil || !p.HasRole(RoleAgent) {
		t.Fatalf("%+v %v", p, err)
	}
	for _, c := range [][2]string{{"kim", "wrong"}, {"nobody", "pw-1234"}} {
		if _, err := l.Login(context.Background(), c[0], c[1]); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%v 는 인증 실패여야 함: %v", c, err)
		}
	}
}

func TestLocalAuthenticateReadsSessionCookie(t *testing.T) {
	s := &Sessions{Secret: []byte("0123456789abcdef")}
	l := &Local{Sessions: s}
	rec := httptest.NewRecorder()
	s.SetCookie(rec, &Principal{Subject: "u", Roles: []string{RoleAgent}})
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(rec.Result().Cookies()[0])
	if p, err := l.Authenticate(r); err != nil || p.Subject != "u" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := l.Authenticate(httptest.NewRequest("GET", "/", nil)); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("쿠키 없으면 인증 실패")
	}
}
