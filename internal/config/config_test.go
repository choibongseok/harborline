package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

var base = map[string]string{"HARBORLINE_DATABASE_URL": "postgres://x", "HARBORLINE_SESSION_SECRET": "0123456789abcdef"}

func with(extra map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestProfileOnpremDefaultsToCustomerSideAdapters(t *testing.T) {
	c, err := FromEnv(env(with(map[string]string{"HARBORLINE_PROFILE": "onprem", "HARBORLINE_SMTP_HOST": "smtp.corp", "HARBORLINE_LLM_BASE_URL": "http://llm.corp"})))
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.Kind != "disk" || c.Mail.Kind != "smtp" || c.LLM.Kind != "internal" {
		t.Fatalf("설치형 기본값이 고객 쪽 어댑터여야 함: %+v %+v %+v", c.Storage, c.Mail, c.LLM)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("유효한 설치형 설정: %v", err)
	}
}

func TestProfileCloudDefaultsToHostedAdapters(t *testing.T) {
	c, _ := FromEnv(env(with(map[string]string{"HARBORLINE_PROFILE": "cloud"})))
	if c.Storage.Kind != "s3" || c.Mail.Kind != "api" || c.LLM.Kind != "hosted" {
		t.Fatalf("클라우드 기본값: %+v %+v %+v", c.Storage, c.Mail, c.LLM)
	}
}

func TestOnpremRejectsCloudAdapters(t *testing.T) {
	c, _ := FromEnv(env(with(map[string]string{
		"HARBORLINE_PROFILE": "onprem", "HARBORLINE_STORAGE": "s3", "HARBORLINE_S3_ENDPOINT": "https://s3", "HARBORLINE_S3_BUCKET": "b",
		"HARBORLINE_MAIL": "api", "HARBORLINE_MAIL_API_URL": "https://m", "HARBORLINE_LLM": "hosted", "HARBORLINE_LLM_BASE_URL": "https://l"})))
	err := c.Validate()
	if err == nil {
		t.Fatal("설치형 프로필에서 클라우드 어댑터를 거부해야 함")
	}
	for _, want := range []string{"s3", "메일 API", "호스팅 LLM"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q 오류가 빠짐: %v", want, err)
		}
	}
}

func TestValidateRequiresAdapterSettings(t *testing.T) {
	c, _ := FromEnv(env(with(map[string]string{"HARBORLINE_AUTH": "ldap", "HARBORLINE_LLM": "off"})))
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "ldap") {
		t.Fatalf("ldap 설정이 비면 거부: %v", err)
	}
	if _, err := FromEnv(env(with(map[string]string{"HARBORLINE_PROFILE": "hybrid"}))); err == nil {
		t.Fatal("알 수 없는 프로필은 거부")
	}
}

func TestParseRoleMap(t *testing.T) {
	m := ParseRoleMap(" support = agent, admins=admin ,broken, =x")
	if m["support"] != "agent" || m["admins"] != "admin" || len(m) != 2 {
		t.Fatalf("%v", m)
	}
}
