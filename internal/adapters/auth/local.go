package auth

import (
	"context"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// UserStore 는 자체 로그인이 쓰는 사용자 저장소다.
type UserStore interface {
	UserHash(ctx context.Context, username string) (hash, role string, err error)
}

// Local 은 자체 로그인(DB 의 bcrypt 해시)이다.
type Local struct {
	Users    UserStore
	Sessions *Sessions
}

func (l *Local) Kind() string { return "local" }

func (l *Local) Login(ctx context.Context, username, password string) (*Principal, error) {
	hash, role, err := l.Users.UserHash(ctx, username)
	if err != nil {
		// 사용자 없음과 비밀번호 틀림을 구분하지 않는다.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5BWoVJ2VxQmL5pXe1Z1g5oS1gYk6a"), []byte(password))
		return nil, ErrUnauthenticated
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrUnauthenticated
	}
	return &Principal{Subject: username, Name: username, Roles: []string{role}}, nil
}

func (l *Local) Authenticate(r *http.Request) (*Principal, error) { return l.Sessions.FromRequest(r) }
