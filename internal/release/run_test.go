package release

import (
	"context"
	"strings"
	"testing"
)

func run(cur string, m Manifest, in Injection) (Result, *SimExecutor) {
	ex := &SimExecutor{In: in}
	return Upgrade(context.Background(), cur, m, ex), ex
}

func last(r Result) Event { return r.Events[len(r.Events)-1] }

func TestUpgradeSucceedsWhenAllStagesPass(t *testing.T) {
	r, ex := run("v1.0.0", mf("v1.1.0", "v1.0.0", SchemaNone), Injection{})
	if r.Outcome != OutcomeSuccess {
		t.Fatalf("성공이어야 함: %+v", r)
	}
	if strings.Contains(strings.Join(ex.Calls, ","), "migrate") {
		t.Fatalf("스키마 변경이 없으면 마이그레이션을 호출하지 않아야 함: %v", ex.Calls)
	}
}

func TestUpgradeRejectsBadSignatureBeforeTouchingAnything(t *testing.T) {
	r, ex := run("v1.0.0", mf("v1.1.0", "v1.0.0", SchemaNone), Injection{BadSignature: true})
	if r.Outcome != OutcomeRejected || len(ex.Calls) != 1 {
		t.Fatalf("서명 불일치는 검증 단계에서 거부하고 다른 단계를 부르지 않아야 함: %+v %v", r, ex.Calls)
	}
}

func TestUpgradeRejectsSkippedVersionBeforeBackup(t *testing.T) {
	r, ex := run("v1.0.0", mf("v1.4.0", "v1.3.0", SchemaNone), Injection{})
	if r.Outcome != OutcomeRejected || strings.Contains(strings.Join(ex.Calls, ","), "backup") {
		t.Fatalf("건너뛴 버전은 백업 전에 거부해야 함: %+v %v", r, ex.Calls)
	}
}

func TestUpgradeAbortsWhenBackupFailsAndKeepsRunningVersion(t *testing.T) {
	r, ex := run("v1.0.0", mf("v1.1.0", "v1.0.0", SchemaNone), Injection{FailAt: StageBackup})
	if r.Outcome != OutcomeAborted || strings.Contains(strings.Join(ex.Calls, ","), "load") {
		t.Fatalf("백업 실패 시 이미지 적재 전에 중단: %+v %v", r, ex.Calls)
	}
}

func TestUpgradeAbortsWhenImageLoadFails(t *testing.T) {
	r, ex := run("v1.0.0", mf("v1.1.0", "v1.0.0", SchemaNone), Injection{FailAt: StageLoad})
	if r.Outcome != OutcomeAborted || strings.Contains(strings.Join(ex.Calls, ","), "switch") {
		t.Fatalf("이미지 적재 실패 시 교체하지 않아야 함: %+v %v", r, ex.Calls)
	}
}

func TestUpgradeAutoRollsBackWhenNoSchemaChangeAndHealthFails(t *testing.T) {
	r, ex := run("v1.0.0", mf("v1.1.0", "v1.0.0", SchemaNone), Injection{FailAt: StageHealth})
	if r.Outcome != OutcomeRolledBack {
		t.Fatalf("스키마 변경 없는 릴리스는 자동 복귀: %+v", r)
	}
	if got := strings.Join(ex.Calls, ","); !strings.HasSuffix(got, "switch-previous,health-previous") {
		t.Fatalf("복귀 뒤 이전 버전 상태 점검까지 해야 성공으로 봄: %v", ex.Calls)
	}
}

func TestUpgradeAutoRollsBackOnMigrationFailureWhenCompatible(t *testing.T) {
	r, _ := run("v1.1.0", mf("v1.2.0", "v1.0.0", SchemaCompatible), Injection{FailAt: StageMigrate})
	if r.Outcome != OutcomeRolledBack {
		t.Fatalf("expand/contract 릴리스는 마이그레이션 실패에도 자동 복귀: %+v", r)
	}
}

func TestUpgradeAutoRollsBackOnHealthFailureWhenCompatible(t *testing.T) {
	r, _ := run("v1.1.0", mf("v1.2.0", "v1.0.0", SchemaCompatible), Injection{FailAt: StageHealth})
	if r.Outcome != OutcomeRolledBack {
		t.Fatalf("expand/contract 릴리스는 상태 점검 실패에도 자동 복귀: %+v", r)
	}
}

func TestUpgradeHaltsWithoutRollbackWhenBreakingSchemaFails(t *testing.T) {
	r, ex := run("v1.2.0", mf("v2.0.0", "v1.2.0", SchemaBreaking), Injection{FailAt: StageHealth})
	if r.Outcome != OutcomeHalted || !strings.Contains(r.Advice, "restore") {
		t.Fatalf("breaking 은 정지 + 복구 안내: %+v", r)
	}
	if strings.Contains(strings.Join(ex.Calls, ","), "switch-previous") {
		t.Fatalf("breaking 은 이전 버전으로 자동 전환하면 안 됨: %v", ex.Calls)
	}
}

func TestUpgradeHaltsWhenRollbackItselfFails(t *testing.T) {
	r, _ := run("v1.0.0", mf("v1.1.0", "v1.0.0", SchemaNone), Injection{FailAt: StageHealth, RollbackBroken: true})
	if r.Outcome != OutcomeHalted || last(r).Stage != StageRollback || last(r).Status != "fail" {
		t.Fatalf("복귀 실패는 정지: %+v", r)
	}
}

func TestUpgradeFreshInstallFailureHaltsWithoutRollback(t *testing.T) {
	r, _ := run("", mf("v1.0.0", "v0.0.0", SchemaCompatible), Injection{FailAt: StageHealth})
	if r.Outcome != OutcomeHalted {
		t.Fatalf("신규 설치 실패는 되돌릴 버전이 없어 정지: %+v", r)
	}
}

func TestUpgradeFreshInstallAlwaysMigratesAndSkipsBackup(t *testing.T) {
	r, ex := run("", mf("v1.1.0", "v1.0.0", SchemaNone), Injection{})
	calls := strings.Join(ex.Calls, ",")
	if r.Outcome != OutcomeSuccess || !strings.Contains(calls, "migrate") || strings.Contains(calls, "backup") {
		t.Fatalf("신규 설치는 스키마 변경이 없는 릴리스라도 전체 스키마를 만들어야 하고 백업은 건너뜀: %+v %v", r, ex.Calls)
	}
}
