// hlbuild 는 릴리스 번들을 만든다: 마이그레이션 규약 검사 → 이미지 빌드 → docker save → 매니페스트 → ed25519 서명.
//
//	hlbuild bundle --out dist/bundles [--release v1.2.0|all]
//	hlbuild lint
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/choibongseok/harborline/internal/release"
)

type relDef struct {
	Version           string         `json:"version"`
	MinFrom           string         `json:"min_from"`
	Schema            release.Schema `json:"schema"`
	Features          string         `json:"features"`
	Break             string         `json:"break"`
	Tamper            string         `json:"tamper"`
	Base              bool           `json:"base"`
	VariantMigrations bool           `json:"variant_migrations"`
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "hlbuild:", err)
		os.Exit(1)
	}
}

func main() {
	if len(os.Args) < 2 {
		die(fmt.Errorf("사용: hlbuild bundle|lint"))
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	out := fs.String("out", "dist/bundles", "번들 출력 폴더")
	only := fs.String("release", "all", "릴리스 이름 또는 all")
	keys := fs.String("keys", "dist/keys", "서명 키 폴더")
	_ = fs.Parse(os.Args[2:])
	switch os.Args[1] {
	case "lint":
		die(lintAll())
	case "bundle":
		die(lintAll())
		die(buildAll(*out, *only, *keys))
	default:
		die(fmt.Errorf("알 수 없는 명령 %s", os.Args[1]))
	}
}

func loadDefs() ([]relDef, error) {
	paths, _ := filepath.Glob("releases/*.json")
	sort.Strings(paths)
	var defs []relDef
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var d relDef
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		defs = append(defs, d)
	}
	return defs, nil
}

func parseMigrations(dir string) ([]release.Migration, error) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
	sort.Strings(paths)
	var ms []release.Migration
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		m, err := release.ParseMigration(filepath.Base(p), string(b))
		if err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	return ms, nil
}

// migrationsFor 는 릴리스 v 에 들어갈 마이그레이션이다: 본 폴더에서 release 가 v 이하인 것 + 릴리스 전용 폴더.
func migrationsFor(d relDef) ([]release.Migration, error) {
	all, err := parseMigrations("migrations")
	if err != nil {
		return nil, err
	}
	v, _ := release.ParseVersion(d.Version)
	var out []release.Migration
	for _, m := range all {
		if mv, _ := release.ParseVersion(m.Release); mv.Compare(v) <= 0 {
			out = append(out, m)
		}
	}
	if d.VariantMigrations {
		extra, err := parseMigrations(filepath.Join("releases", d.Version, "migrations"))
		if err != nil {
			return nil, err
		}
		out = append(out, extra...)
	}
	return out, nil
}

// lintAll 은 모든 릴리스의 마이그레이션이 expand → contract 규약을 지키고, 선언한 schema 가 계산값과 같은지 본다.
func lintAll() error {
	defs, err := loadDefs()
	if err != nil {
		return err
	}
	bad := 0
	for _, d := range defs {
		ms, err := migrationsFor(d)
		if err != nil {
			return err
		}
		for _, v := range release.CheckDeclared(d.Schema, ms, d.Version) {
			fmt.Printf("규약 위반 %s: %s\n", d.Version, v)
			bad++
		}
	}
	if bad > 0 {
		return fmt.Errorf("마이그레이션 규약 위반 %d건: 번들을 만들지 않음", bad)
	}
	fmt.Printf("마이그레이션 규약 검사 통과 (릴리스 %d개)\n", len(defs))
	return nil
}

func loadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("키 파일 형식 오류: %s", path)
		}
		return ed25519.PrivateKey(raw), nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return nil, err
	}
	pubPath := strings.TrimSuffix(path, ".key") + ".pub"
	return priv, os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644)
}

func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

func sha(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func buildAll(out, only, keyDir string) error {
	defs, err := loadDefs()
	if err != nil {
		return err
	}
	priv, err := loadOrCreateKey(filepath.Join(keyDir, "release.key"))
	if err != nil {
		return err
	}
	untrusted, err := loadOrCreateKey(filepath.Join(keyDir, "untrusted.key"))
	if err != nil {
		return err
	}
	ctx := filepath.Join("dist", "build", "ctx")
	if err := os.MkdirAll(ctx, 0o755); err != nil {
		return err
	}
	fmt.Println("== 정적 바이너리 빌드 (linux/" + runtime.GOARCH + ")")
	c := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(ctx, "harborline"), "./cmd/harborline")
	c.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return err
	}
	if b, err := os.ReadFile("deploy/Dockerfile"); err == nil {
		if err := os.WriteFile(filepath.Join(ctx, "Dockerfile"), b, 0o644); err != nil {
			return err
		}
	}
	for _, d := range defs {
		if only != "all" && only != d.Version {
			continue
		}
		if err := buildOne(d, out, priv, untrusted, ctx); err != nil {
			return fmt.Errorf("%s: %w", d.Version, err)
		}
	}
	return nil
}

func buildOne(d relDef, out string, priv, untrusted ed25519.PrivateKey, ctx string) error {
	fmt.Println("== 번들", d.Version)
	dir := filepath.Join(out, "harborline-"+d.Version)
	_ = os.RemoveAll(dir)
	for _, sub := range []string{"images", "migrations"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	tag := "harborline-app:" + d.Version
	if err := run("docker", "build", "-q", "-t", tag, "--build-arg", "VERSION="+d.Version, "--build-arg", "FEATURES="+d.Features, "--build-arg", "BREAK="+d.Break, ctx); err != nil {
		return err
	}
	tar := filepath.Join("images", "harborline-app-"+d.Version+".tar")
	if err := run("docker", "save", "-o", filepath.Join(dir, tar), tag); err != nil {
		return err
	}
	files := []string{tar}
	if d.Base {
		pg := filepath.Join("images", "postgres-16-alpine.tar")
		if err := run("docker", "save", "-o", filepath.Join(dir, pg), "postgres:16-alpine"); err != nil {
			return err
		}
		files = append(files, pg)
	}
	ms, err := migrationsFor(d)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if err := os.WriteFile(filepath.Join(dir, "migrations", m.File), []byte(m.SQL), 0o644); err != nil {
			return err
		}
		files = append(files, filepath.Join("migrations", m.File))
	}
	man := release.Manifest{Name: "harborline", Version: d.Version, MinFrom: d.MinFrom, Schema: d.Schema}
	for _, f := range files {
		s, err := sha(filepath.Join(dir, f))
		if err != nil {
			return err
		}
		man.Files = append(man.Files, release.FileEntry{Path: f, SHA256: s})
	}
	raw, err := man.Marshal()
	if err != nil {
		return err
	}
	signer := priv
	if d.Tamper == "signature" {
		signer = untrusted // 매니페스트는 멀쩡하지만 신뢰하는 키로 서명되지 않음
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.sig"), []byte(release.Sign(signer, raw)+"\n"), 0o644); err != nil {
		return err
	}
	if d.Tamper == "checksum" {
		f, err := os.OpenFile(filepath.Join(dir, tar), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, _ = f.Write([]byte{0})
		f.Close()
	}
	return nil
}
