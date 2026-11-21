package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDC 는 OpenID Connect 공급자(예: 사내 Keycloak)로 인증하고 그룹 클레임을 역할로 매핑한다.
type OIDC struct {
	Provider    *oidc.Provider
	Verifier    *oidc.IDTokenVerifier
	OAuth       *oauth2.Config
	GroupsClaim string
	RoleMap     map[string]string
	Sessions    *Sessions
	HTTP        *http.Client // 프록시·사설 CA 가 적용된 클라이언트
}

// NewOIDC 는 발급자 디스커버리를 해서 어댑터를 만든다.
func NewOIDC(ctx context.Context, hc *http.Client, issuer, clientID, secret, redirect, groupsClaim string, roleMap map[string]string, s *Sessions) (*OIDC, error) {
	ctx = oidc.ClientContext(ctx, hc)
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc 디스커버리: %w", err)
	}
	return &OIDC{
		Provider: p, Verifier: p.Verifier(&oidc.Config{ClientID: clientID}),
		OAuth:       &oauth2.Config{ClientID: clientID, ClientSecret: secret, Endpoint: p.Endpoint(), RedirectURL: redirect, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}},
		GroupsClaim: groupsClaim, RoleMap: roleMap, Sessions: s, HTTP: hc,
	}, nil
}

func (o *OIDC) Kind() string { return "oidc" }

// PrincipalFromIDToken 은 ID 토큰을 검증하고 그룹을 역할로 바꾼다.
func (o *OIDC) PrincipalFromIDToken(ctx context.Context, raw string) (*Principal, error) {
	ctx = oidc.ClientContext(ctx, o.HTTP)
	tok, err := o.Verifier.Verify(ctx, raw)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	claims := map[string]any{}
	if err := tok.Claims(&claims); err != nil {
		return nil, ErrUnauthenticated
	}
	var groups []string
	if arr, ok := claims[o.GroupsClaim].([]any); ok {
		for _, g := range arr {
			if s, ok := g.(string); ok {
				groups = append(groups, strings.TrimPrefix(s, "/"))
			}
		}
	}
	roles, err := MapRoles(groups, o.RoleMap)
	if err != nil {
		return nil, err
	}
	name, _ := claims["preferred_username"].(string)
	if name == "" {
		name = tok.Subject
	}
	return &Principal{Subject: tok.Subject, Name: name, Roles: roles}, nil
}

// Authenticate 는 세션 쿠키 또는 Authorization: Bearer <id_token> 으로 확인한다.
func (o *OIDC) Authenticate(r *http.Request) (*Principal, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return o.PrincipalFromIDToken(r.Context(), strings.TrimPrefix(h, "Bearer "))
	}
	return o.Sessions.FromRequest(r)
}

// LoginRedirect 는 공급자 로그인 화면으로 보낸다(state 쿠키 포함).
func (o *OIDC) LoginRedirect(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: "hl_state", Value: state, Path: "/", HttpOnly: true, MaxAge: 600, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, o.OAuth.AuthCodeURL(state), http.StatusFound)
}

// Callback 은 인가 코드를 토큰으로 바꾸고 세션을 발급한다.
func (o *OIDC) Callback(w http.ResponseWriter, r *http.Request) (*Principal, error) {
	sc, err := r.Cookie("hl_state")
	if err != nil || sc.Value == "" || sc.Value != r.URL.Query().Get("state") {
		return nil, errors.New("state 불일치")
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, o.HTTP)
	tok, err := o.OAuth.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		return nil, fmt.Errorf("토큰 교환: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	p, err := o.PrincipalFromIDToken(r.Context(), raw)
	if err != nil {
		return nil, err
	}
	o.Sessions.SetCookie(w, p)
	return p, nil
}
