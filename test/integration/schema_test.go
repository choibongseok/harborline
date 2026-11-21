//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choibongseok/harborline/internal/migrate"
)

func TestMigrationsApplyOnceAndAreIdempotent(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, db)
	must(t, err)
	defer pool.Close()
	files := migrateFiles(t, "0001_init.sql", "0002_inquiry_state.sql")
	got, err := migrate.Apply(ctx, pool, files)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	got, err = migrate.Apply(ctx, pool, files)
	if err != nil || len(got) != 0 {
		t.Fatalf("두 번째 적용은 아무것도 하지 않아야 함: %v %v", got, err)
	}
}

func TestMigrationFailureMidwayKeepsEarlierFilesAndRollsBackTheFailedOne(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	pool, _ := pgxpool.New(ctx, db)
	defer pool.Close()
	files := migrateFiles(t, "0001_init.sql")
	files = append(files,
		migrate.File{Name: "0002_ok.sql", SQL: "ALTER TABLE inquiries ADD COLUMN assignee text;"},
		migrate.File{Name: "0003_bad.sql", SQL: "ALTER TABLE inquiries ADD COLUMN half_done text;\nSELECT * FROM table_that_does_not_exist;"})
	applied, err := migrate.Apply(ctx, pool, files)
	if err == nil || len(applied) != 2 {
		t.Fatalf("앞선 두 파일은 적용되고 세 번째에서 실패해야 함: %v %v", applied, err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='inquiries' AND column_name IN ('assignee','half_done')`).Scan(&n)
	if n != 1 {
		t.Fatalf("실패한 파일은 통째로 롤백되어야 함(assignee 만 남음): %d", n)
	}
}

func TestAppliedMigrationThatChangedIsRejected(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	pool, _ := pgxpool.New(ctx, db)
	defer pool.Close()
	files := migrateFiles(t, "0001_init.sql")
	_, err := migrate.Apply(ctx, pool, files)
	must(t, err)
	files[0].SQL += "\n-- 몰래 수정"
	if _, err := migrate.Apply(ctx, pool, files); err == nil || !strings.Contains(err.Error(), "바뀌었음") {
		t.Fatalf("적용 뒤 바뀐 마이그레이션은 거부해야 함: %v", err)
	}
}

// expand 마이그레이션이 적용된 스키마에서도 이전 버전(v1.1) 앱이 그대로 동작한다 — 자동 롤백이 가능한 이유.
func TestPreviousAppVersionStillWorksOnExpandedSchema(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql", "0002_inquiry_state.sql")
	old := start(t, baseConfig(db), "stats", "", nil) // v1.1: status 만 쓴다
	id := old.createInquiry(t, "구버전이 쓴 문의", "내용")
	old.login(t, "admin", "admin-pw")
	if code, _, raw := old.do(t, "POST", "/api/inquiries/"+id+"/replies", map[string]string{"body": "답변"}); code != 201 {
		t.Fatalf("%d %s", code, raw)
	}
	// 새 버전(v1.2)이 같은 행을 읽으면 트리거가 맞춰 둔 state 가 보인다.
	cur := start(t, baseConfig(db), "stats,state", "", nil)
	cur.login(t, "admin", "admin-pw")
	_, m, _ := cur.do(t, "GET", "/api/inquiries/"+id, nil)
	if m["state"] != float64(1) || m["status"] != "answered" {
		t.Fatalf("구버전이 쓴 행의 state 가 동기화되어야 함: %v", m)
	}
}

func TestNewAppVersionFailsWithoutItsMigration(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql") // 마이그레이션을 건너뜀
	e := start(t, baseConfig(db), "stats,state", "", nil)
	if code, _, _ := e.do(t, "POST", "/api/inquiries", map[string]string{"subject": "s", "body": "b", "email": "a@b.test"}); code != 500 {
		t.Fatalf("마이그레이션 없이 새 버전을 띄우면 쓰기가 실패해야 함: %d", code)
	}
}

func TestBreakingSchemaBreaksThePreviousVersionButNotTheNewOne(t *testing.T) {
	db := newDB(t)
	applyMigrations(t, db, "0001_init.sql", "0002_inquiry_state.sql", "0003_drop_status.sql")
	prev := start(t, baseConfig(db), "stats,state", "", nil)
	if code, _, _ := prev.do(t, "POST", "/api/inquiries", map[string]string{"subject": "s", "body": "b", "email": "a@b.test"}); code != 500 {
		t.Fatalf("status 컬럼이 사라지면 이전 버전은 쓸 수 없어야 함(그래서 자동 롤백 불가): %d", code)
	}
	cur := start(t, baseConfig(db), "stats,state,state-only", "", nil)
	cur.createInquiry(t, "새 버전", "정상")
}
