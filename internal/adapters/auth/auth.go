// Package auth 는 인증 어댑터다(자체 로그인 ↔ OIDC ↔ LDAP). 코어는 Principal 과 역할만 안다.
package auth

import (
	"context"
	"errors"
	"net/http"
)

// 역할.
const (
	RoleAgent = "agent"
	RoleAdmin = "admin"
)

// Principal 은 인증된 사용자다.
type Principal struct {
	Subject string   `json:"subject"`
	Name    string   `json:"name"`
	Roles   []string `json:"roles"`
}

// HasRole 은 역할 보유 여부다(admin 은 agent 를 포함한다).
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role || (r == RoleAdmin && role == RoleAgent) {
			return true
		}
	}
	return false
}

// 인증 오류.
var (
	ErrUnauthenticated = errors.New("인증 필요")
	ErrNoRole          = errors.New("매핑된 역할이 없음")
)

// Authenticator 는 코어가 아는 인증 인터페이스다.
type Authenticator interface {
	Kind() string
	// Authenticate 는 요청의 세션 쿠키·Bearer 토큰으로 사용자를 확인한다.
	Authenticate(r *http.Request) (*Principal, error)
}

// PasswordLogin 은 아이디·비밀번호 로그인을 지원하는 어댑터(local, ldap)다.
type PasswordLogin interface {
	Login(ctx context.Context, username, password string) (*Principal, error)
}

// MapRoles 는 그룹 목록을 역할 목록으로 바꾼다. 매핑된 그룹이 없으면 ErrNoRole.
func MapRoles(groups []string, roleMap map[string]string) ([]string, error) {
	seen := map[string]bool{}
	var roles []string
	for _, g := range groups {
		if r, ok := roleMap[g]; ok && !seen[r] {
			seen[r] = true
			roles = append(roles, r)
		}
	}
	if len(roles) == 0 {
		return nil, ErrNoRole
	}
	return roles, nil
}
