package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workspace(t *testing.T, relJSON string, migrations map[string]string) {
	t.Helper()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "migrations"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "releases"), 0o755)
	for n, s := range migrations {
		_ = os.WriteFile(filepath.Join(dir, "migrations", n), []byte(s), 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "releases", "r.json"), []byte(relJSON), 0o644)
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestBuildStopsWhenAnExpandMigrationBreaksTheOldVersion(t *testing.T) {
	workspace(t, `{"version":"v1.1.0","min_from":"v1.0.0","schema":"compatible"}`, map[string]string{
		"0001_x.sql": "-- release: v1.1.0\n-- phase: expand\nALTER TABLE a DROP COLUMN b;\n"})
	err := lintAll()
	if err == nil || !strings.Contains(err.Error(), "규약 위반") {
		t.Fatalf("규약을 어긴 마이그레이션은 번들 빌드 전에 걸려야 함: %v", err)
	}
}

func TestBuildStopsWhenDeclaredSchemaDiffersFromMigrations(t *testing.T) {
	workspace(t, `{"version":"v1.1.0","min_from":"v1.0.0","schema":"none"}`, map[string]string{
		"0001_x.sql": "-- release: v1.1.0\n-- phase: expand\nCREATE TABLE a (id int);\n"})
	if err := lintAll(); err == nil {
		t.Fatal("스키마 변경이 있는데 none 으로 선언하면 거부해야 함")
	}
}

func TestLintPassesForCompliantRelease(t *testing.T) {
	workspace(t, `{"version":"v1.1.0","min_from":"v1.0.0","schema":"compatible"}`, map[string]string{
		"0001_x.sql": "-- release: v1.1.0\n-- phase: expand\nCREATE TABLE a (id int);\n"})
	if err := lintAll(); err != nil {
		t.Fatal(err)
	}
}
