// Package migrate 는 번들에 들어 있는 SQL 마이그레이션을 순서대로, 파일마다 한 트랜잭션으로 적용한다.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// File 은 마이그레이션 파일 하나다.
type File struct{ Name, SQL string }

// LoadDir 은 디렉터리의 *.sql 을 이름순으로 읽는다.
func LoadDir(dir string) ([]File, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []File
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, File{filepath.Base(p), string(b)})
	}
	return out, nil
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// Apply 는 아직 적용되지 않은 파일을 적용한다. 이미 적용된 파일의 내용이 바뀌었으면 오류다.
// 도중에 실패하면 그때까지 적용한 파일 이름과 오류를 돌려준다(앞선 파일은 커밋된 상태로 남는다).
func Apply(ctx context.Context, pool *pgxpool.Pool, files []File) ([]string, error) {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (file text PRIMARY KEY, sha256 text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("schema_migrations: %w", err)
	}
	var applied []string
	for _, f := range files {
		var have string
		err := pool.QueryRow(ctx, `SELECT sha256 FROM schema_migrations WHERE file=$1`, f.Name).Scan(&have)
		if err == nil {
			if have != sum(f.SQL) {
				return applied, fmt.Errorf("%s: 이미 적용된 마이그레이션의 내용이 바뀌었음", f.Name)
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return applied, err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return applied, err
		}
		if _, err := tx.Exec(ctx, f.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("%s: %w", f.Name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(file, sha256) VALUES ($1,$2)`, f.Name, sum(f.SQL)); err != nil {
			_ = tx.Rollback(ctx)
			return applied, err
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied = append(applied, f.Name)
	}
	return applied, nil
}

// Latest 는 마지막으로 적용된 파일 이름이다(없으면 빈 문자열).
func Latest(ctx context.Context, pool *pgxpool.Pool) string {
	var f string
	if err := pool.QueryRow(ctx, `SELECT file FROM schema_migrations ORDER BY file DESC LIMIT 1`).Scan(&f); err != nil {
		return ""
	}
	return strings.TrimSuffix(f, ".sql")
}
