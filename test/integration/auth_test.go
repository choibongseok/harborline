//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/choibongseok/harborline/internal/config"
)

const issuer = "http://localhost:58480/realms/harborline"

func ldapCfg(db string) config.Config {
	cfg := baseConfig(db)
	cfg.Auth = config.Auth{Kind: "ldap", LDAP: config.LDAP{
		URL: "ldap://127.0.0.1:53890", BindDN: "cn=admin,dc=harborline,dc=test", BindPassword: "adminpw",
		UserBaseDN: "ou=people,dc=harborline,dc=test", UserFilter: "(uid=%s)",
		GroupBaseDN: "ou=groups,dc=harborline,dc=test", GroupFilter: "(member=%s)",
		RoleMap: map[string]string{"support": "agent", "admins": "admin"}}}
	return cfg
}

func TestLDAPLoginMapsGroupsToRoles(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	e := start(t, ldapCfg(db), "", "", nil)
	if code := e.login(t, "alice", "alicepw"); code != 200 {
		t.Fatalf("support 그룹 사용자 로그인: %d", code)
	}
	if code, _, _ := e.do(t, "GET", "/api/inquiries", nil); code != 200 {
		t.Fatalf("agent 역할로 목록 조회: %d", code)
	}
	if code, m, _ := e.do(t, "GET", "/api/system", nil); code != 200 || m["adapters"].(map[string]any)["auth"] != "ldap" {
		t.Fatalf("%v", m)
	}
}

func TestLDAPRejectsWrongPasswordUnmappedGroupAndInjection(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	e := start(t, ldapCfg(db), "", "", nil)
	if code := e.login(t, "alice", "wrong"); code != 401 {
		t.Fatalf("틀린 비밀번호: %d", code)
	}
	if code := e.login(t, "carol", "carolpw"); code != 403 {
		t.Fatalf("그룹이 매핑되지 않은 사용자는 403: %d", code)
	}
	if code, _, _ := e.do(t, "GET", "/api/inquiries", nil); code != 401 {
		t.Fatalf("403 사용자는 세션이 없어야 함: %d", code)
	}
	if code := e.login(t, "*", "x"); code != 401 {
		t.Fatalf("필터 문자(*)로 아무나 찾으면 안 됨: %d", code)
	}
	if code := e.login(t, "alice)(uid=*", "alicepw"); code != 401 {
		t.Fatalf("LDAP 필터 주입: %d", code)
	}
	if code := e.login(t, "alice", ""); code != 401 {
		t.Fatalf("빈 비밀번호는 익명 바인드로 통과하면 안 됨: %d", code)
	}
}

func waitKeycloak(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(issuer + "/.well-known/openid-configuration")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("Keycloak(58480)이 준비되지 않음: make infra 필요")
}

func oidcCfg(db string) config.Config {
	cfg := baseConfig(db)
	cfg.Auth = config.Auth{Kind: "oidc", OIDC: config.OIDC{Issuer: issuer, ClientID: "harborline", ClientSecret: "client-secret",
		GroupsClaim: "groups", RoleMap: map[string]string{"support": "agent", "admins": "admin"}}}
	return cfg
}

func idToken(t *testing.T, user, pw string) string {
	t.Helper()
	resp, err := http.PostForm("http://localhost:58480/realms/harborline/protocol/openid-connect/token", url.Values{
		"grant_type": {"password"}, "client_id": {"harborline"}, "client_secret": {"client-secret"}, "username": {user}, "password": {pw}, "scope": {"openid"}})
	must(t, err)
	defer resp.Body.Close()
	var m map[string]any
	must(t, json.NewDecoder(resp.Body).Decode(&m))
	tok, _ := m["id_token"].(string)
	if tok == "" {
		t.Fatalf("id_token 없음: %v", m)
	}
	return tok
}

func (e *env) bearer(t *testing.T, tok, path string) int {
	req, _ := http.NewRequest("GET", e.URL+path, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{}).Do(req)
	must(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

func TestOIDCBearerTokenMapsGroupClaimToRole(t *testing.T) {
	waitKeycloak(t)
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	e := start(t, oidcCfg(db), "", "", nil)
	if code := e.bearer(t, idToken(t, "alice", "alicepw"), "/api/inquiries"); code != 200 {
		t.Fatalf("support 그룹 토큰: %d", code)
	}
	if code := e.bearer(t, idToken(t, "bob", "bobpw"), "/api/inquiries"); code != 200 {
		t.Fatalf("admins 그룹 토큰: %d", code)
	}
}

func TestOIDCRejectsUnmappedGroupBadTokenAndMissingToken(t *testing.T) {
	waitKeycloak(t)
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	e := start(t, oidcCfg(db), "", "", nil)
	if code := e.bearer(t, idToken(t, "carol", "carolpw"), "/api/inquiries"); code != 403 {
		t.Fatalf("그룹이 없는 사용자는 403: %d", code)
	}
	tok := idToken(t, "alice", "alicepw")
	if code := e.bearer(t, tok[:len(tok)-4]+"AAAA", "/api/inquiries"); code != 401 {
		t.Fatalf("서명이 깨진 토큰은 401: %d", code)
	}
	if code := e.bearer(t, "", "/api/inquiries"); code != 401 {
		t.Fatalf("토큰 없음은 401: %d", code)
	}
}

// stripSecure 는 Keycloak 이 http 로컬 환경에서도 붙이는 Secure 속성을 떼어 Go 의 쿠키 항아리가 쿠키를 받게 한다(시험 전용).
type stripSecure struct{ next http.RoundTripper }

func (s stripSecure) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := s.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	h := resp.Header.Values("Set-Cookie")
	resp.Header.Del("Set-Cookie")
	for _, c := range h {
		resp.Header.Add("Set-Cookie", secureAttr.ReplaceAllString(c, ""))
	}
	return resp, nil
}

var secureAttr = regexp.MustCompile(`;\s*Secure`)

var formAction = regexp.MustCompile(`<form[^>]*id="kc-form-login"[^>]*action="([^"]+)"`)

// 브라우저 대신 쿠키 항아리가 있는 클라이언트로 인가 코드 흐름 전체(로그인 폼 제출 포함)를 따라간다.
func TestOIDCAuthorizationCodeFlowIssuesSession(t *testing.T) {
	waitKeycloak(t)
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql")
	e := start(t, oidcCfg(db), "", "", nil)
	e.Client.Transport = stripSecure{http.DefaultTransport}
	resp, err := e.Client.Get(e.URL + "/auth/login")
	must(t, err)
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := formAction.FindSubmatch(page)
	if m == nil {
		t.Fatalf("Keycloak 로그인 폼을 찾지 못함(%d): %.300s", resp.StatusCode, page)
	}
	action := html.UnescapeString(string(m[1]))
	resp, err = e.Client.PostForm(action, url.Values{"username": {"alice"}, "password": {"alicepw"}, "credentialId": {""}})
	must(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"roles":["agent"]`) {
		t.Fatalf("콜백이 역할이 담긴 사용자를 돌려줘야 함: %d %.1500s", resp.StatusCode, body)
	}
	if code, _, _ := e.do(t, "GET", "/api/inquiries", nil); code != 200 {
		t.Fatalf("콜백이 발급한 세션 쿠키로 조회: %d", code)
	}
}

var _ = context.Background
