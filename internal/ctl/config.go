// Package ctl 은 harborctl 의 실행부다. 판정은 internal/release 가 하고, 여기는 도커·파일을 만지는 Executor 를 구현한다.
package ctl

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Mount 는 앱 컨테이너에 읽기 전용으로 붙이는 호스트 파일이다(사설 CA 등).
type Mount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
}

// TLSCheck 는 doctor 가 앱 네트워크 안에서 확인할 TLS 종단이다.
type TLSCheck struct {
	Addr     string `json:"addr"`
	StartTLS string `json:"starttls"`
	CAFile   string `json:"ca_file"` // 컨테이너 안 경로
}

// Network 는 스택이 붙을 도커 네트워크다.
type Network struct {
	Name     string `json:"name"`
	External bool   `json:"external"` // true 면 이미 있는 네트워크에 붙는다
	Internal bool   `json:"internal"` // External 이 false 일 때 외부로 나가는 길이 없는 네트워크로 만든다
}

// Config 는 고객 환경별 설치 설정(install.json)이다.
type Config struct {
	Project          string            `json:"project"`
	Network          Network           `json:"network"`
	DNS              []string          `json:"dns"`
	Env              map[string]string `json:"env"`
	Mounts           []Mount           `json:"mounts"`
	Ports            []string          `json:"ports"` // 예: "127.0.0.1:8080:8080"
	HealthTimeoutSec int               `json:"health_timeout_sec"`
	MinDiskGB        int               `json:"min_disk_gb"`
	CAFiles          []string          `json:"ca_files"`
	TLSChecks        []TLSCheck        `json:"tls_checks"`
	PGPassword       string            `json:"pg_password"`
	SessionSecret    string            `json:"session_secret"`
}

// State 는 설치 상태(state.json)다.
type State struct {
	Version        string `json:"version"`
	Schema         string `json:"schema"` // 현재 릴리스가 스키마에 한 일(none|compatible|breaking)
	Previous       string `json:"previous"`
	PreviousSchema string `json:"previous_schema"`
	Status         string `json:"status"` // running | halted
	LastBackup     string `json:"last_backup"`
	Note           string `json:"note"`
}

func rnd(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// LoadConfig 는 설치 폴더의 install.json 을 읽는다.
func LoadConfig(dir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(dir, "install.json"))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("install.json: %w", err)
	}
	return c, c.validate()
}

func (c *Config) validate() error {
	if c.Project == "" {
		return errors.New("project 가 필요함")
	}
	if c.Network.Name == "" {
		c.Network.Name = c.Project
	}
	if c.HealthTimeoutSec == 0 {
		c.HealthTimeoutSec = 60
	}
	if c.MinDiskGB == 0 {
		c.MinDiskGB = 2
	}
	return nil
}

// Save 는 설정을 설치 폴더에 쓴다(비밀 값은 비어 있으면 만든다).
func (c *Config) Save(dir string) error {
	if c.PGPassword == "" {
		c.PGPassword = rnd(16)
	}
	if c.SessionSecret == "" {
		c.SessionSecret = rnd(24)
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(filepath.Join(dir, "install.json"), b, 0o600)
}

// LoadState 는 state.json 을 읽는다(없으면 빈 상태).
func LoadState(dir string) State {
	var s State
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Save 는 상태를 쓴다.
func (s State) Save(dir string) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, "state.json"), b, 0o644)
}
