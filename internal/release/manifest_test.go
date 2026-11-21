package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func opener(files map[string]string) func(string) (io.ReadCloser, error) {
	return func(p string) (io.ReadCloser, error) {
		c, ok := files[p]
		if !ok {
			return nil, errors.New("없음")
		}
		return io.NopCloser(strings.NewReader(c)), nil
	}
}

func TestSignatureVerifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw, _ := mf("v1.1.0", "v1.0.0", SchemaNone).Marshal()
	if err := VerifySignature(pub, raw, Sign(priv, raw)); err != nil {
		t.Fatalf("정상 서명은 통과해야 함: %v", err)
	}
}

func TestSignatureRejectsTamperedManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw, _ := mf("v1.1.0", "v1.0.0", SchemaNone).Marshal()
	sig := Sign(priv, raw)
	tampered := []byte(strings.Replace(string(raw), `"none"`, `"breaking"`, 1))
	if err := VerifySignature(pub, tampered, sig); !errors.Is(err, ErrSignature) {
		t.Fatalf("변조된 매니페스트는 거부해야 함: %v", err)
	}
}

func TestSignatureRejectsOtherKeyAndGarbage(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	raw := []byte("{}")
	if err := VerifySignature(pub, raw, Sign(other, raw)); !errors.Is(err, ErrSignature) {
		t.Fatalf("다른 키 서명: %v", err)
	}
	if err := VerifySignature(pub, raw, "%%%not-base64"); !errors.Is(err, ErrSignature) {
		t.Fatalf("깨진 서명 문자열: %v", err)
	}
}

func TestManifestMarshalIsDeterministic(t *testing.T) {
	a := Manifest{Name: "h", Version: "v1.0.0", MinFrom: "v0.0.0", Schema: SchemaNone,
		Files: []FileEntry{{"b", "2"}, {"a", "1"}}}
	b := Manifest{Name: "h", Version: "v1.0.0", MinFrom: "v0.0.0", Schema: SchemaNone,
		Files: []FileEntry{{"a", "1"}, {"b", "2"}}}
	ra, _ := a.Marshal()
	rb, _ := b.Marshal()
	if string(ra) != string(rb) {
		t.Fatal("파일 순서가 달라도 같은 바이트가 나와야 서명이 재현됨")
	}
}

func TestVerifyFilesDetectsChecksumMismatch(t *testing.T) {
	m := Manifest{Files: []FileEntry{{"images/app.tar", sum("원본")}}}
	err := VerifyFiles(m, opener(map[string]string{"images/app.tar": "변조"}))
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("체크섬 불일치를 잡아야 함: %v", err)
	}
}

func TestVerifyFilesDetectsMissingFile(t *testing.T) {
	m := Manifest{Files: []FileEntry{{"migrations/0001.sql", sum("x")}}}
	if err := VerifyFiles(m, opener(nil)); !errors.Is(err, ErrMissing) {
		t.Fatalf("없는 파일을 잡아야 함: %v", err)
	}
}

func TestVerifyFilesPassesWhenAllMatch(t *testing.T) {
	m := Manifest{Files: []FileEntry{{"a", sum("1")}, {"b", sum("2")}}}
	if err := VerifyFiles(m, opener(map[string]string{"a": "1", "b": "2", "extra": "x"})); err != nil {
		t.Fatalf("일치하면 통과해야 함: %v", err)
	}
}

func TestParseManifestValidatesFields(t *testing.T) {
	if _, err := ParseManifest([]byte(`{"version":"v1.0.0","min_from":"v0.0.0","schema":"weird"}`)); err == nil {
		t.Fatal("알 수 없는 schema 값은 거부해야 함")
	}
	if _, err := ParseManifest([]byte(`{"version":"v1.0.0","min_from":"x","schema":"none"}`)); err == nil {
		t.Fatal("min_from 형식 오류는 거부해야 함")
	}
	if _, err := ParseManifest([]byte(`{"version":"v1.0.0","min_from":"v0.0.0","schema":"none"}`)); err != nil {
		t.Fatalf("정상 매니페스트: %v", err)
	}
}
