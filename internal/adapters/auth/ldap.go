package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// LDAP 는 디렉터리 서버에 바인드해서 인증하고 그룹을 역할로 매핑한다.
type LDAP struct {
	URL          string
	BindDN       string // 검색용 서비스 계정(비면 익명 검색)
	BindPassword string
	UserBaseDN   string
	UserFilter   string // 예: (uid=%s)
	GroupBaseDN  string
	GroupFilter  string // 예: (member=%s) — %s 는 사용자 DN
	StartTLS     bool
	Roots        *x509.CertPool
	RoleMap      map[string]string
	Sessions     *Sessions
}

func (l *LDAP) Kind() string { return "ldap" }

func (l *LDAP) dial() (*ldap.Conn, error) {
	tc := &tls.Config{RootCAs: l.Roots, MinVersion: tls.VersionTLS12}
	c, err := ldap.DialURL(l.URL, ldap.DialWithTLSConfig(tc))
	if err != nil {
		return nil, fmt.Errorf("ldap 연결: %w", err)
	}
	if l.StartTLS {
		host := strings.TrimPrefix(strings.TrimPrefix(l.URL, "ldap://"), "ldaps://")
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		tc.ServerName = host
		if err := c.StartTLS(tc); err != nil {
			c.Close()
			return nil, fmt.Errorf("ldap STARTTLS: %w", err)
		}
	}
	return c, nil
}

func (l *LDAP) Login(_ context.Context, username, password string) (*Principal, error) {
	if username == "" || password == "" {
		return nil, ErrUnauthenticated // 빈 비밀번호는 익명 바인드로 통과하므로 먼저 막는다
	}
	c, err := l.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if l.BindDN != "" {
		if err := c.Bind(l.BindDN, l.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap 서비스 계정 바인드: %w", err)
		}
	}
	res, err := c.Search(ldap.NewSearchRequest(l.UserBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 0, false,
		fmt.Sprintf(l.UserFilter, ldap.EscapeFilter(username)), []string{"dn", "cn", "uid"}, nil))
	if err != nil || len(res.Entries) != 1 {
		return nil, ErrUnauthenticated
	}
	userDN := res.Entries[0].DN
	if err := c.Bind(userDN, password); err != nil {
		return nil, ErrUnauthenticated
	}
	if l.BindDN != "" {
		if err := c.Bind(l.BindDN, l.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap 서비스 계정 재바인드: %w", err)
		}
	}
	gres, err := c.Search(ldap.NewSearchRequest(l.GroupBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf(l.GroupFilter, ldap.EscapeFilter(userDN)), []string{"cn"}, nil))
	if err != nil {
		return nil, fmt.Errorf("ldap 그룹 조회: %w", err)
	}
	var groups []string
	for _, e := range gres.Entries {
		groups = append(groups, e.GetAttributeValue("cn"))
	}
	roles, err := MapRoles(groups, l.RoleMap)
	if err != nil {
		return nil, err
	}
	name := res.Entries[0].GetAttributeValue("cn")
	if name == "" {
		name = username
	}
	return &Principal{Subject: username, Name: name, Roles: roles}, nil
}

func (l *LDAP) Authenticate(r *http.Request) (*Principal, error) { return l.Sessions.FromRequest(r) }
