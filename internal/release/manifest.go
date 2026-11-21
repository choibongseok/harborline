package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Schema 는 이 릴리스가 DB 스키마에 하는 일의 종류다.
type Schema string

const (
	// SchemaNone 은 스키마 변경이 없다.
	SchemaNone Schema = "none"
	// SchemaCompatible 은 expand/contract 규약을 지킨 변경이다. 직전 버전 앱이 변경된 스키마에서도 돈다.
	SchemaCompatible Schema = "compatible"
	// SchemaBreaking 은 직전 버전 앱이 돌지 못할 수 있는 변경이다.
	SchemaBreaking Schema = "breaking"
)

// FileEntry 는 번들 안 파일 하나와 SHA-256 이다.
type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Manifest 는 번들의 목차다. 서명은 manifest.json 원문 바이트에 대해 한다.
type Manifest struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	MinFrom string      `json:"min_from"` // 업그레이드 가능한 최소 기존 버전
	Schema  Schema      `json:"schema"`
	Files   []FileEntry `json:"files"`
}

// Marshal 은 파일 순서를 고정해 같은 내용이면 같은 바이트가 나오게 한다.
func (m Manifest) Marshal() ([]byte, error) {
	c := m
	c.Files = append([]FileEntry(nil), m.Files...)
	sort.Slice(c.Files, func(i, j int) bool { return c.Files[i].Path < c.Files[j].Path })
	return json.MarshalIndent(c, "", "  ")
}

// ParseManifest 는 manifest.json 을 읽고 필수 필드를 확인한다.
func ParseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("매니페스트 파싱 실패: %w", err)
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return m, err
	}
	if _, err := ParseVersion(m.MinFrom); err != nil {
		return m, fmt.Errorf("min_from: %w", err)
	}
	switch m.Schema {
	case SchemaNone, SchemaCompatible, SchemaBreaking:
	default:
		return m, fmt.Errorf("schema 값이 올바르지 않음: %q", m.Schema)
	}
	return m, nil
}

// 검증 실패 종류.
var (
	ErrSignature = errors.New("서명이 맞지 않음")
	ErrChecksum  = errors.New("체크섬이 맞지 않음")
	ErrMissing   = errors.New("매니페스트에 있는 파일이 번들에 없음")
)

// Sign 은 manifest 원문에 ed25519 서명을 하고 base64 로 돌려준다.
func Sign(priv ed25519.PrivateKey, manifestRaw []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifestRaw))
}

// VerifySignature 는 서명을 확인한다.
func VerifySignature(pub ed25519.PublicKey, manifestRaw []byte, sigB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrSignature
	}
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, manifestRaw, sig) {
		return ErrSignature
	}
	return nil
}

// VerifyFiles 는 매니페스트의 모든 파일을 open 으로 열어 SHA-256 을 대조한다.
// 매니페스트에 없는 파일은 보지 않는다(번들 폴더에 다른 파일이 있어도 설치 대상이 아니다).
func VerifyFiles(m Manifest, open func(path string) (io.ReadCloser, error)) error {
	for _, f := range m.Files {
		rc, err := open(f.Path)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrMissing, f.Path)
		}
		h := sha256.New()
		_, cerr := io.Copy(h, rc)
		rc.Close()
		if cerr != nil {
			return fmt.Errorf("%s 읽기 실패: %w", f.Path, cerr)
		}
		if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("%w: %s", ErrChecksum, f.Path)
		}
	}
	return nil
}
