package ctl

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/choibongseok/harborline/internal/release"
)

// Installer 는 설치 폴더 하나를 다룬다.
type Installer struct {
	Dir  string
	Cfg  Config
	Pub  ed25519.PublicKey
	Out  io.Writer
	Poll time.Duration // 상태 점검 간격(시험에서 줄인다)
}

func (in *Installer) say(f string, a ...any) { fmt.Fprintf(in.Out, f+"\n", a...) }

func (in *Installer) docker(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "docker", args...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr, c.Stdin = &out, &errb, stdin
	if err := c.Run(); err != nil {
		return out.String(), fmt.Errorf("docker %s: %w: %s", strings.Join(args[:min(len(args), 3)], " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (in *Installer) compose(ctx context.Context, args ...string) error {
	_, err := in.docker(ctx, nil, append([]string{"compose", "-p", in.Cfg.Project, "-f", filepath.Join(in.Dir, "compose.yml")}, args...)...)
	return err
}

func (in *Installer) appName() string { return in.Cfg.Project + "-app" }
func (in *Installer) dbName() string  { return in.Cfg.Project + "-db" }
func (in *Installer) attachDir() string {
	abs, _ := filepath.Abs(filepath.Join(in.Dir, "data", "attachments"))
	return abs
}

func (in *Installer) writeCompose(version string) error {
	if err := os.MkdirAll(in.attachDir(), 0o777); err != nil {
		return err
	}
	_ = os.Chmod(in.attachDir(), 0o777) // 컨테이너의 비특권 사용자(10001)가 쓸 수 있어야 한다
	b, err := RenderCompose(in.Cfg, version, in.attachDir())
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(in.Dir, "compose.yml"), b, 0o644)
}

// ---- 번들 읽기 ----

// Bundle 은 읽어 둔 번들이다.
type Bundle struct {
	Dir      string
	Raw      []byte
	Sig      string
	Manifest release.Manifest
}

// OpenBundle 은 manifest.json·manifest.sig 를 읽는다(검증은 Upgrade 의 verify 단계에서 한다).
func OpenBundle(dir string) (*Bundle, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("번들에 manifest.json 이 없음: %w", err)
	}
	sig, err := os.ReadFile(filepath.Join(dir, "manifest.sig"))
	if err != nil {
		return nil, fmt.Errorf("번들에 manifest.sig 가 없음: %w", err)
	}
	m, err := release.ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	return &Bundle{Dir: dir, Raw: raw, Sig: strings.TrimSpace(string(sig)), Manifest: m}, nil
}

// ---- Executor ----

type exec_ struct {
	in     *Installer
	b      *Bundle
	cur    State
	backup string
}

func (e *exec_) Verify(ctx context.Context) error {
	if err := release.VerifySignature(e.in.Pub, e.b.Raw, e.b.Sig); err != nil {
		return err
	}
	return release.VerifyFiles(e.b.Manifest, func(p string) (io.ReadCloser, error) { return os.Open(filepath.Join(e.b.Dir, p)) })
}

func (e *exec_) Backup(ctx context.Context) error {
	id, err := e.in.Backup(ctx)
	e.backup = id
	return err
}

func (e *exec_) LoadImages(ctx context.Context) error {
	for _, f := range e.b.Manifest.Files {
		if strings.HasPrefix(f.Path, "images/") && strings.HasSuffix(f.Path, ".tar") {
			if _, err := e.in.docker(ctx, nil, "load", "-i", filepath.Join(e.b.Dir, f.Path)); err != nil {
				return err
			}
			e.in.say("  이미지 적재: %s", f.Path)
		}
	}
	return nil
}

func (e *exec_) dbURL() string {
	c := e.in.Cfg
	return fmt.Sprintf("postgres://harborline:%s@%s-db:5432/harborline?sslmode=disable", c.PGPassword, c.Project)
}

func (e *exec_) Migrate(ctx context.Context) error {
	if err := e.in.writeCompose(e.b.Manifest.Version); err != nil {
		return err
	}
	if err := e.in.compose(ctx, "up", "-d", "--wait", "db"); err != nil {
		return err
	}
	abs, _ := filepath.Abs(filepath.Join(e.b.Dir, "migrations"))
	out, err := e.in.docker(ctx, nil, "run", "--rm", "--network", e.in.Cfg.Network.Name,
		"-e", "HARBORLINE_DATABASE_URL="+e.dbURL(), "-v", abs+":/migrations:ro", "harborline-app:"+e.b.Manifest.Version, "migrate", "--dir", "/migrations")
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			e.in.say("  %s", l)
		}
	}
	return err
}

func (e *exec_) version(t release.Target) string {
	if t == release.TargetPrevious {
		return e.cur.Version
	}
	return e.b.Manifest.Version
}

func (e *exec_) Switch(ctx context.Context, t release.Target) error {
	if err := e.in.writeCompose(e.version(t)); err != nil {
		return err
	}
	return e.in.compose(ctx, "up", "-d", "--wait", "--force-recreate", "--no-deps", "--wait-timeout", "5", "app")
}

func (e *exec_) Health(ctx context.Context, t release.Target) error {
	return e.in.WaitHealthy(ctx)
}

// WaitHealthy 는 앱 컨테이너 안에서 /healthz 를 확인하며 제한 시간까지 기다린다.
func (in *Installer) WaitHealthy(ctx context.Context) error {
	poll := in.Poll
	if poll == 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(time.Duration(in.Cfg.HealthTimeoutSec) * time.Second)
	var last error
	for {
		if _, err := in.docker(ctx, nil, "exec", in.appName(), "/harborline", "healthcheck"); err == nil {
			return nil
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d초 안에 상태 점검을 통과하지 못함: %v", in.Cfg.HealthTimeoutSec, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// ---- 설치·업그레이드 ----

// Upgrade 는 번들을 설치하거나(상태가 비어 있으면) 업그레이드한다. 판정은 release.Upgrade 가 한다.
func (in *Installer) Upgrade(ctx context.Context, bundleDir string) (release.Result, error) {
	b, err := OpenBundle(bundleDir)
	if err != nil {
		return release.Result{}, err
	}
	cur := LoadState(in.Dir)
	if cur.Status == "halted" {
		return release.Result{}, errors.New("이전 업그레이드가 정지 상태로 끝났음: `harborctl restore` 로 복구한 뒤 진행하세요")
	}
	ex := &exec_{in: in, b: b, cur: cur}
	res := release.Upgrade(ctx, cur.Version, b.Manifest, ex)
	for _, ev := range res.Events {
		in.say("[%-4s] %-8s %s", ev.Status, ev.Stage, ev.Detail)
	}
	next := cur
	switch res.Outcome {
	case release.OutcomeSuccess:
		next = State{Version: b.Manifest.Version, Schema: string(b.Manifest.Schema), Previous: cur.Version, PreviousSchema: cur.Schema, Status: "running", LastBackup: ex.backup}
	case release.OutcomeHalted:
		_ = in.compose(ctx, "stop", "app")
		next.Status, next.LastBackup = "halted", ex.backup
		next.Note = fmt.Sprintf("%s 로 올리다 정지", b.Manifest.Version)
	case release.OutcomeRolledBack, release.OutcomeAborted:
		if ex.backup != "" {
			next.LastBackup = ex.backup
		}
	}
	if err := next.Save(in.Dir); err != nil {
		return res, err
	}
	if res.Advice != "" {
		in.say("판정: %s — %s", res.Outcome, res.Advice)
	} else {
		in.say("판정: %s", res.Outcome)
	}
	return res, nil
}

// Rollback 은 앱만 직전 버전으로 되돌린다. 스키마 변경이 호환되지 않으면 거부한다.
func (in *Installer) Rollback(ctx context.Context) error {
	st := LoadState(in.Dir)
	if st.Previous == "" {
		return errors.New("되돌릴 이전 버전이 없음")
	}
	if ok, why := release.CanRollback(release.Schema(st.Schema)); !ok {
		return fmt.Errorf("롤백할 수 없음: %s. `harborctl restore` 로 직전 백업을 복구하세요", why)
	}
	in.say("롤백: %s → %s (%s)", st.Version, st.Previous, func() string { _, w := release.CanRollback(release.Schema(st.Schema)); return w }())
	if err := in.writeCompose(st.Previous); err != nil {
		return err
	}
	if err := in.compose(ctx, "up", "-d", "--wait", "--force-recreate", "--no-deps", "--wait-timeout", "5", "app"); err != nil {
		return err
	}
	if err := in.WaitHealthy(ctx); err != nil {
		return fmt.Errorf("이전 버전이 상태 점검을 통과하지 못함: %w", err)
	}
	next := State{Version: st.Previous, Schema: st.PreviousSchema, Status: "running", LastBackup: st.LastBackup}
	return next.Save(in.Dir)
}

// ---- 백업·복구 ----

type backupMeta struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Schema      string `json:"schema"`
	CreatedAt   string `json:"created_at"`
	DBSHA256    string `json:"db_sha256"`
	AttachSHA   string `json:"attachments_sha256"`
	AttachFiles int    `json:"attachment_files"`
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
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

// Backup 은 DB 덤프 + 설정 + 첨부를 backups/<id>/ 에 만든다.
func (in *Installer) Backup(ctx context.Context) (string, error) {
	st := LoadState(in.Dir)
	id := time.Now().UTC().Format("20060102T150405.000Z")
	dir := filepath.Join(in.Dir, "backups", id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	dump, err := in.docker(ctx, nil, "exec", in.dbName(), "pg_dump", "-U", "harborline", "-Fc", "harborline")
	if err != nil {
		return "", fmt.Errorf("DB 덤프: %w", err)
	}
	if len(dump) == 0 {
		return "", errors.New("DB 덤프가 비어 있음")
	}
	if err := os.WriteFile(filepath.Join(dir, "db.dump"), []byte(dump), 0o640); err != nil {
		return "", err
	}
	n, err := tarDir(in.attachDir(), filepath.Join(dir, "attachments.tgz"))
	if err != nil {
		return "", fmt.Errorf("첨부 백업: %w", err)
	}
	for _, f := range []string{"install.json", "state.json"} {
		if b, err := os.ReadFile(filepath.Join(in.Dir, f)); err == nil {
			_ = os.WriteFile(filepath.Join(dir, f), b, 0o600)
		}
	}
	ds, _ := sha256File(filepath.Join(dir, "db.dump"))
	as, _ := sha256File(filepath.Join(dir, "attachments.tgz"))
	meta := backupMeta{ID: id, Version: st.Version, Schema: st.Schema, CreatedAt: time.Now().UTC().Format(time.RFC3339), DBSHA256: ds, AttachSHA: as, AttachFiles: n}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), mb, 0o644); err != nil {
		return "", err
	}
	in.say("  백업 %s (DB %d 바이트, 첨부 %d개)", id, len(dump), n)
	return id, nil
}

func tarDir(src, dst string) (int, error) {
	f, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	n := 0
	err = filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		h, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			r, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, r)
			r.Close()
			if err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	return n, gz.Close()
}

func untarTo(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p := filepath.Join(dst, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(p, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("백업 안의 경로가 범위를 벗어남: %s", h.Name)
		}
		if h.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o777); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			return err
		}
		w, err := os.Create(p)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return err
		}
		w.Close()
	}
}

// Backups 는 백업 id 목록(오래된 순)이다.
func (in *Installer) Backups() []string {
	es, _ := os.ReadDir(filepath.Join(in.Dir, "backups"))
	var ids []string
	for _, e := range es {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// Restore 는 백업을 되돌리고 그 시점의 버전으로 다시 올린다. 백업 파일 체크섬을 먼저 검증한다.
func (in *Installer) Restore(ctx context.Context, id string) error {
	ids := in.Backups()
	if id == "" || id == "latest" {
		if len(ids) == 0 {
			return errors.New("백업이 없음")
		}
		id = ids[len(ids)-1]
	}
	dir := filepath.Join(in.Dir, "backups", id)
	mb, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return fmt.Errorf("백업 %s 를 찾을 수 없음: %w", id, err)
	}
	var meta backupMeta
	if err := json.Unmarshal(mb, &meta); err != nil {
		return err
	}
	if got, _ := sha256File(filepath.Join(dir, "db.dump")); got != meta.DBSHA256 {
		return errors.New("백업의 DB 덤프 체크섬이 맞지 않음: 복구하지 않음")
	}
	if got, _ := sha256File(filepath.Join(dir, "attachments.tgz")); got != meta.AttachSHA {
		return errors.New("백업의 첨부 체크섬이 맞지 않음: 복구하지 않음")
	}
	in.say("복구: 백업 %s (버전 %s)", id, meta.Version)
	_ = in.compose(ctx, "stop", "app")
	if err := in.compose(ctx, "up", "-d", "--wait", "db"); err != nil {
		return err
	}
	psql := func(sql string) error {
		_, err := in.docker(ctx, nil, "exec", in.dbName(), "psql", "-U", "harborline", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c", sql)
		return err
	}
	if err := psql("DROP DATABASE IF EXISTS harborline WITH (FORCE)"); err != nil {
		return err
	}
	if err := psql("CREATE DATABASE harborline"); err != nil {
		return err
	}
	df, err := os.Open(filepath.Join(dir, "db.dump"))
	if err != nil {
		return err
	}
	defer df.Close()
	if _, err := in.docker(ctx, df, "exec", "-i", in.dbName(), "pg_restore", "-U", "harborline", "-d", "harborline", "--no-owner", "--exit-on-error"); err != nil {
		return fmt.Errorf("DB 복원: %w", err)
	}
	if err := os.RemoveAll(in.attachDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(in.attachDir(), 0o777); err != nil {
		return err
	}
	if err := untarTo(filepath.Join(dir, "attachments.tgz"), in.attachDir()); err != nil {
		return err
	}
	_ = os.Chmod(in.attachDir(), 0o777)
	if meta.Version == "" {
		return errors.New("백업에 버전 정보가 없어 앱을 올릴 수 없음")
	}
	if err := in.writeCompose(meta.Version); err != nil {
		return err
	}
	if err := in.compose(ctx, "up", "-d", "--wait", "--force-recreate", "--no-deps", "--wait-timeout", "5", "app"); err != nil {
		return err
	}
	if err := in.WaitHealthy(ctx); err != nil {
		return fmt.Errorf("복구한 버전 %s 이 상태 점검을 통과하지 못함: %w", meta.Version, err)
	}
	st := State{Version: meta.Version, Schema: meta.Schema, Status: "running", LastBackup: id}
	in.say("복구 완료: %s 상태 점검 통과", meta.Version)
	return st.Save(in.Dir)
}

// Destroy 는 컨테이너·볼륨·설치 폴더 내용을 지운다(시험·재설치용).
func (in *Installer) Destroy(ctx context.Context) error {
	_ = in.compose(ctx, "down", "-v", "--remove-orphans")
	for _, p := range []string{"data", "backups", "state.json", "compose.yml"} {
		_ = os.RemoveAll(filepath.Join(in.Dir, p))
	}
	return nil
}
