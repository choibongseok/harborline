// Package config 는 설정을 한 곳에서 읽는다. HARBORLINE_PROFILE 하나로 클라우드/설치형 기본값이 갈린다.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 프로필.
const (
	ProfileCloud  = "cloud"
	ProfileOnprem = "onprem"
)

// Config 는 서버 전체 설정이다.
type Config struct {
	Profile       string
	Listen        string
	DatabaseURL   string
	Version       string
	SessionSecret string
	AdminPassword string

	Storage Storage
	Mail    Mail
	Auth    Auth
	LLM     LLM
	HTTP    HTTP
}

// Storage 는 첨부 저장소 설정이다. Kind: disk | s3
type Storage struct {
	Kind string
	Dir  string
	S3   S3
}

// S3 는 S3 호환 저장소 설정이다.
type S3 struct{ Endpoint, Bucket, Region, AccessKey, SecretKey string }

// Mail 은 메일 발송 설정이다. Kind: smtp | api
type Mail struct {
	Kind string
	From string
	SMTP SMTP
	API  MailAPI
}

// SMTP 는 고객 SMTP 서버 설정이다.
type SMTP struct {
	Host, Username, Password string
	Port                     int
	StartTLS                 bool
}

// MailAPI 는 클라우드 메일 API 설정이다.
type MailAPI struct{ URL, Token string }

// Auth 는 인증 설정이다. Kind: local | oidc | ldap
type Auth struct {
	Kind string
	OIDC OIDC
	LDAP LDAP
}

// OIDC 설정. RoleMap: 그룹 이름 → 역할(agent|admin)
type OIDC struct {
	Issuer, ClientID, ClientSecret, RedirectURL, GroupsClaim string
	RoleMap                                                  map[string]string
}

// LDAP 설정.
type LDAP struct {
	URL, BindDN, BindPassword, UserBaseDN, UserFilter, GroupBaseDN, GroupFilter string
	StartTLS                                                                    bool
	RoleMap                                                                     map[string]string
}

// LLM 은 답변 초안 제안에 쓰는 모델 설정이다. Kind: internal | hosted | off
type LLM struct {
	Kind    string
	BaseURL string
	APIKey  string
	Model   string
}

// HTTP 는 나가는 HTTP(S) 호출 공통 설정이다.
type HTTP struct {
	ProxyURL string
	NoProxy  string
	CAFile   string
}

// FromEnv 는 환경 변수에서 설정을 만든다.
func FromEnv(get func(string) string) (Config, error) {
	g := func(k, def string) string {
		if v := get(k); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Profile:       g("HARBORLINE_PROFILE", ProfileOnprem),
		Listen:        g("HARBORLINE_LISTEN", ":8080"),
		DatabaseURL:   get("HARBORLINE_DATABASE_URL"),
		Version:       g("HARBORLINE_VERSION", "dev"),
		SessionSecret: get("HARBORLINE_SESSION_SECRET"),
		AdminPassword: get("HARBORLINE_ADMIN_PASSWORD"),
	}
	if c.Profile != ProfileCloud && c.Profile != ProfileOnprem {
		return c, fmt.Errorf("HARBORLINE_PROFILE 은 cloud|onprem: %q", c.Profile)
	}
	onprem := c.Profile == ProfileOnprem
	pick := func(onpremDef, cloudDef string) string {
		if onprem {
			return onpremDef
		}
		return cloudDef
	}
	c.Storage = Storage{
		Kind: g("HARBORLINE_STORAGE", pick("disk", "s3")),
		Dir:  g("HARBORLINE_STORAGE_DIR", "/data/attachments"),
		S3: S3{
			Endpoint: get("HARBORLINE_S3_ENDPOINT"), Bucket: get("HARBORLINE_S3_BUCKET"),
			Region: g("HARBORLINE_S3_REGION", "us-east-1"), AccessKey: get("HARBORLINE_S3_ACCESS_KEY"), SecretKey: get("HARBORLINE_S3_SECRET_KEY"),
		},
	}
	port, _ := strconv.Atoi(g("HARBORLINE_SMTP_PORT", "587"))
	c.Mail = Mail{
		Kind: g("HARBORLINE_MAIL", pick("smtp", "api")),
		From: g("HARBORLINE_MAIL_FROM", "support@harborline.invalid"),
		SMTP: SMTP{Host: get("HARBORLINE_SMTP_HOST"), Port: port, StartTLS: g("HARBORLINE_SMTP_STARTTLS", "true") == "true",
			Username: get("HARBORLINE_SMTP_USER"), Password: get("HARBORLINE_SMTP_PASSWORD")},
		API: MailAPI{URL: get("HARBORLINE_MAIL_API_URL"), Token: get("HARBORLINE_MAIL_API_TOKEN")},
	}
	c.Auth = Auth{
		Kind: g("HARBORLINE_AUTH", "local"),
		OIDC: OIDC{Issuer: get("HARBORLINE_OIDC_ISSUER"), ClientID: get("HARBORLINE_OIDC_CLIENT_ID"), ClientSecret: get("HARBORLINE_OIDC_CLIENT_SECRET"),
			RedirectURL: get("HARBORLINE_OIDC_REDIRECT_URL"), GroupsClaim: g("HARBORLINE_OIDC_GROUPS_CLAIM", "groups"), RoleMap: ParseRoleMap(get("HARBORLINE_OIDC_ROLE_MAP"))},
		LDAP: LDAP{URL: get("HARBORLINE_LDAP_URL"), BindDN: get("HARBORLINE_LDAP_BIND_DN"), BindPassword: get("HARBORLINE_LDAP_BIND_PASSWORD"),
			UserBaseDN: get("HARBORLINE_LDAP_USER_BASE_DN"), UserFilter: g("HARBORLINE_LDAP_USER_FILTER", "(uid=%s)"),
			GroupBaseDN: get("HARBORLINE_LDAP_GROUP_BASE_DN"), GroupFilter: g("HARBORLINE_LDAP_GROUP_FILTER", "(member=%s)"),
			StartTLS: get("HARBORLINE_LDAP_STARTTLS") == "true", RoleMap: ParseRoleMap(get("HARBORLINE_LDAP_ROLE_MAP"))},
	}
	c.LLM = LLM{Kind: g("HARBORLINE_LLM", pick("internal", "hosted")), BaseURL: get("HARBORLINE_LLM_BASE_URL"),
		APIKey: get("HARBORLINE_LLM_API_KEY"), Model: g("HARBORLINE_LLM_MODEL", "default")}
	c.HTTP = HTTP{ProxyURL: get("HARBORLINE_HTTPS_PROXY"), NoProxy: get("HARBORLINE_NO_PROXY"), CAFile: get("HARBORLINE_CA_FILE")}
	return c, nil
}

// ParseRoleMap 은 "그룹=역할,그룹=역할" 을 읽는다.
func ParseRoleMap(s string) map[string]string {
	m := map[string]string{}
	for _, p := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && k != "" && v != "" {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// Validate 는 프로필과 어댑터 조합이 맞는지 본다. 설치형 프로필은 외부 서비스 어댑터를 거부한다.
func (c Config) Validate() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("HARBORLINE_DATABASE_URL 이 필요함"))
	}
	if len(c.SessionSecret) < 16 {
		errs = append(errs, errors.New("HARBORLINE_SESSION_SECRET 은 16자 이상이어야 함"))
	}
	if c.Profile == ProfileOnprem {
		if c.Storage.Kind == "s3" {
			errs = append(errs, errors.New("설치형 프로필에서는 s3 저장소를 쓸 수 없음(disk 사용)"))
		}
		if c.Mail.Kind == "api" {
			errs = append(errs, errors.New("설치형 프로필에서는 클라우드 메일 API 를 쓸 수 없음(smtp 사용)"))
		}
		if c.LLM.Kind == "hosted" {
			errs = append(errs, errors.New("설치형 프로필에서는 호스팅 LLM 을 쓸 수 없음(internal 또는 off)"))
		}
	}
	switch c.Storage.Kind {
	case "disk":
	case "s3":
		if c.Storage.S3.Endpoint == "" || c.Storage.S3.Bucket == "" {
			errs = append(errs, errors.New("s3 저장소는 endpoint/bucket 이 필요함"))
		}
	default:
		errs = append(errs, fmt.Errorf("알 수 없는 저장소: %q", c.Storage.Kind))
	}
	switch c.Mail.Kind {
	case "smtp":
		if c.Mail.SMTP.Host == "" {
			errs = append(errs, errors.New("smtp 메일은 HARBORLINE_SMTP_HOST 가 필요함"))
		}
	case "api":
		if c.Mail.API.URL == "" {
			errs = append(errs, errors.New("api 메일은 HARBORLINE_MAIL_API_URL 이 필요함"))
		}
	default:
		errs = append(errs, fmt.Errorf("알 수 없는 메일: %q", c.Mail.Kind))
	}
	switch c.Auth.Kind {
	case "local":
	case "oidc":
		if c.Auth.OIDC.Issuer == "" || c.Auth.OIDC.ClientID == "" {
			errs = append(errs, errors.New("oidc 는 issuer/client id 가 필요함"))
		}
	case "ldap":
		if c.Auth.LDAP.URL == "" || c.Auth.LDAP.UserBaseDN == "" {
			errs = append(errs, errors.New("ldap 은 url/user base dn 이 필요함"))
		}
	default:
		errs = append(errs, fmt.Errorf("알 수 없는 인증: %q", c.Auth.Kind))
	}
	switch c.LLM.Kind {
	case "off":
	case "internal", "hosted":
		if c.LLM.BaseURL == "" {
			errs = append(errs, errors.New("LLM 은 HARBORLINE_LLM_BASE_URL 이 필요함(쓰지 않으려면 HARBORLINE_LLM=off)"))
		}
	default:
		errs = append(errs, fmt.Errorf("알 수 없는 LLM: %q", c.LLM.Kind))
	}
	return errors.Join(errs...)
}
