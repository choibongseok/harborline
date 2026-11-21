package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// CookieName 은 세션 쿠키 이름이다.
const CookieName = "hl_session"

// Sessions 는 서명된 쿠키 세션이다(서버 쪽 저장소 없음).
type Sessions struct {
	Secret []byte
	TTL    time.Duration
	Now    func() time.Time
}

type sessionBody struct {
	P   Principal `json:"p"`
	Exp int64     `json:"exp"`
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sessions) mac(payload string) string {
	m := hmac.New(sha256.New, s.Secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Issue 는 쿠키 값을 만든다.
func (s *Sessions) Issue(p *Principal) string {
	ttl := s.TTL
	if ttl == 0 {
		ttl = 8 * time.Hour
	}
	b, _ := json.Marshal(sessionBody{P: *p, Exp: s.now().Add(ttl).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.mac(payload)
}

// Parse 는 쿠키 값을 검증하고 사용자를 돌려준다.
func (s *Sessions) Parse(v string) (*Principal, error) {
	payload, sig, ok := strings.Cut(v, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(payload))) {
		return nil, ErrUnauthenticated
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	var b sessionBody
	if json.Unmarshal(raw, &b) != nil || s.now().Unix() > b.Exp {
		return nil, errors.New("세션 만료")
	}
	return &b.P, nil
}

// FromRequest 는 요청의 세션 쿠키에서 사용자를 읽는다.
func (s *Sessions) FromRequest(r *http.Request) (*Principal, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	return s.Parse(c.Value)
}

// SetCookie 는 응답에 세션 쿠키를 건다.
func (s *Sessions) SetCookie(w http.ResponseWriter, p *Principal) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: s.Issue(p), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
