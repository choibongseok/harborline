package ctl

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/choibongseok/harborline/internal/pki"
)

func sampleConfig() Config {
	c := Config{Project: "hl", Network: Network{Name: "hl-site", External: true}, DNS: []string{"10.0.0.2"},
		Env: map[string]string{"HARBORLINE_SMTP_HOST": "smtp.corp.test"}, Mounts: []Mount{{"/etc/ca.pem", "/etc/harborline/ca.pem"}},
		PGPassword: "pw", SessionSecret: "0123456789abcdef"}
	_ = c.validate()
	return c
}

func TestComposeJoinsExistingNetworkAndPublishesNoPortsByDefault(t *testing.T) {
	b, err := RenderCompose(sampleConfig(), "v1.2.0", "/data/att")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"image: harborline-app:v1.2.0", "external: true", "- 10.0.0.2", `HARBORLINE_SMTP_HOST: "smtp.corp.test"`, "read_only: true", "/etc/ca.pem:/etc/harborline/ca.pem:ro"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q 가 없음", want)
		}
	}
	if strings.Contains(s, "ports:") {
		t.Error("기본은 호스트 포트를 열지 않아야 함")
	}
}

func TestComposeCreatesInternalNetworkWhenAskedAndLetsEnvOverrideDefaults(t *testing.T) {
	c := sampleConfig()
	c.Network = Network{Name: "hl-closed", Internal: true}
	c.Env["HARBORLINE_PROFILE"] = "cloud"
	b, _ := RenderCompose(c, "v1.0.0", "/a")
	s := string(b)
	if !strings.Contains(s, "internal: true") || strings.Contains(s, "external: true") {
		t.Errorf("내부 네트워크:\n%s", s)
	}
	if strings.Contains(s, `HARBORLINE_PROFILE: "onprem"`) || !strings.Contains(s, `HARBORLINE_PROFILE: "cloud"`) {
		t.Errorf("설정의 env 가 기본값을 덮어야 함:\n%s", s)
	}
}

func TestCheckCAFileClassifiesValidWarnAndExpired(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, ca *pki.CA) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, ca.PEM, 0o644)
		return p
	}
	ok, _ := pki.NewCA("ok", time.Now().Add(90*24*time.Hour))
	soon, _ := pki.NewCA("soon", time.Now().Add(10*24*time.Hour))
	old, _ := pki.NewCA("old", time.Now().Add(-time.Hour))
	if c := CheckCAFile(write("ok.pem", ok), time.Now()); c.Status != "ok" {
		t.Errorf("%+v", c)
	}
	if c := CheckCAFile(write("soon.pem", soon), time.Now()); c.Status != "warn" {
		t.Errorf("%+v", c)
	}
	if c := CheckCAFile(write("old.pem", old), time.Now()); c.Status != "fail" || !strings.Contains(c.Detail, "만료됨") {
		t.Errorf("%+v", c)
	}
	if c := CheckCAFile(filepath.Join(dir, "none.pem"), time.Now()); c.Status != "fail" {
		t.Errorf("%+v", c)
	}
}

func TestAttachmentArchiveRoundTripAndRejectsPathEscape(t *testing.T) {
	src := t.TempDir()
	_ = os.MkdirAll(filepath.Join(src, "q1"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "q1", "a.txt"), []byte("첨부"), 0o644)
	arc := filepath.Join(t.TempDir(), "a.tgz")
	n, err := tarDir(src, arc)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	dst := t.TempDir()
	if err := untarTo(arc, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "q1", "a.txt")); string(b) != "첨부" {
		t.Fatalf("%q", b)
	}
	// 경로를 벗어나는 항목이 든 아카이브는 거부한다.
	evil := filepath.Join(t.TempDir(), "evil.tgz")
	f, _ := os.Create(evil)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	f.Close()
	if err := untarTo(evil, t.TempDir()); err == nil {
		t.Fatal("범위를 벗어난 경로는 거부해야 함")
	}
}

func TestStateAndConfigPersistWithGeneratedSecrets(t *testing.T) {
	dir := t.TempDir()
	c := Config{Project: "hl"}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(dir)
	if err != nil || len(got.PGPassword) < 16 || len(got.SessionSecret) < 16 || got.Network.Name != "hl" || got.HealthTimeoutSec != 60 {
		t.Fatalf("%+v %v", got, err)
	}
	if err := (State{Version: "v1.0.0", Status: "running"}).Save(dir); err != nil {
		t.Fatal(err)
	}
	if s := LoadState(dir); s.Version != "v1.0.0" {
		t.Fatalf("%+v", s)
	}
	if _, err := LoadConfig(t.TempDir()); err == nil {
		t.Fatal("설정이 없으면 오류")
	}
}
