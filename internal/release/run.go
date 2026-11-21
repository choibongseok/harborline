package release

import (
	"context"
	"errors"
	"fmt"
)

// Stage 는 업그레이드 단계다.
type Stage string

const (
	StageVerify   Stage = "verify"
	StageGate     Stage = "gate"
	StageBackup   Stage = "backup"
	StageLoad     Stage = "load"
	StageMigrate  Stage = "migrate"
	StageSwitch   Stage = "switch"
	StageHealth   Stage = "health"
	StageRollback Stage = "rollback"
)

// Target 은 어느 쪽 버전을 대상으로 하는지 나타낸다.
type Target string

const (
	TargetNew      Target = "new"
	TargetPrevious Target = "previous"
)

// Executor 는 단계의 실제 실행을 맡는다. harborctl 은 도커로, 브라우저 데모는 가짜로 구현한다.
type Executor interface {
	Verify(ctx context.Context) error // 서명 + 파일 체크섬
	Backup(ctx context.Context) error
	LoadImages(ctx context.Context) error
	Migrate(ctx context.Context) error // 스키마 변경이 없는 릴리스에서는 호출하지 않는다
	Switch(ctx context.Context, t Target) error
	Health(ctx context.Context, t Target) error
}

// Outcome 은 업그레이드의 최종 상태다.
type Outcome string

const (
	OutcomeSuccess    Outcome = "success"     // 새 버전이 상태 점검까지 통과
	OutcomeRejected   Outcome = "rejected"    // 서명·체크섬·게이트에서 거부. 아무것도 바꾸지 않음
	OutcomeAborted    Outcome = "aborted"     // 백업·이미지 적재 실패. 운영 중인 버전은 그대로
	OutcomeRolledBack Outcome = "rolled_back" // 실패했지만 이전 버전으로 복귀하고 상태 점검까지 통과
	OutcomeHalted     Outcome = "halted"      // 자동 복귀 불가 또는 복귀 실패. 정지, 백업 복구 필요
)

// Event 는 타임라인 한 줄이다.
type Event struct {
	Stage  Stage  `json:"stage"`
	Status string `json:"status"` // ok | fail | skip
	Detail string `json:"detail"`
}

// Result 는 Upgrade 의 결과다.
type Result struct {
	Outcome Outcome `json:"outcome"`
	Events  []Event `json:"events"`
	Advice  string  `json:"advice"`
}

type runner struct {
	res Result
}

func (r *runner) add(s Stage, status, detail string) {
	r.res.Events = append(r.res.Events, Event{s, status, detail})
}

// Upgrade 는 current(빈 문자열이면 신규 설치)에서 m 으로 올리는 전체 절차를 돈다.
// 판정은 모두 여기서 하고, 부수 효과는 ex 가 한다.
func Upgrade(ctx context.Context, current string, m Manifest, ex Executor) Result {
	r := &runner{}
	fin := func(o Outcome, advice string) Result {
		r.res.Outcome, r.res.Advice = o, advice
		return r.res
	}

	if err := ex.Verify(ctx); err != nil {
		r.add(StageVerify, "fail", err.Error())
		return fin(OutcomeRejected, "번들이 변조되었거나 손상되었습니다. 설치하지 않았고 시스템은 그대로입니다. 번들을 다시 받으세요.")
	}
	r.add(StageVerify, "ok", "서명과 파일 체크섬 일치")

	g := Gate(current, m)
	if !g.Allowed {
		r.add(StageGate, "fail", g.Message)
		return fin(OutcomeRejected, "이 릴리스로는 올릴 수 없습니다. 시스템은 그대로입니다.")
	}
	r.add(StageGate, "ok", g.Message)

	if current == "" {
		r.add(StageBackup, "skip", "신규 설치는 백업할 대상이 없음")
	} else if err := ex.Backup(ctx); err != nil {
		r.add(StageBackup, "fail", err.Error())
		return fin(OutcomeAborted, "백업에 실패해 시작하지 않았습니다. 운영 중인 버전은 그대로입니다.")
	} else {
		r.add(StageBackup, "ok", "DB·설정·첨부 백업 완료")
	}

	if err := ex.LoadImages(ctx); err != nil {
		r.add(StageLoad, "fail", err.Error())
		return fin(OutcomeAborted, "이미지를 적재하지 못했습니다. 운영 중인 버전은 그대로입니다.")
	}
	r.add(StageLoad, "ok", "컨테이너 이미지 적재")

	var stepErr error
	var failed Stage
	if m.Schema == SchemaNone && current != "" {
		r.add(StageMigrate, "skip", "스키마 변경 없음")
	} else if err := ex.Migrate(ctx); err != nil {
		r.add(StageMigrate, "fail", err.Error())
		stepErr, failed = err, StageMigrate
	} else {
		r.add(StageMigrate, "ok", fmt.Sprintf("마이그레이션 적용 (%s)", m.Schema))
	}

	if stepErr == nil {
		if err := ex.Switch(ctx, TargetNew); err != nil {
			r.add(StageSwitch, "fail", err.Error())
			stepErr, failed = err, StageSwitch
		} else {
			r.add(StageSwitch, "ok", "새 버전으로 교체")
		}
	}
	if stepErr == nil {
		if err := ex.Health(ctx, TargetNew); err != nil {
			r.add(StageHealth, "fail", err.Error())
			stepErr, failed = err, StageHealth
		} else {
			r.add(StageHealth, "ok", "상태 점검 통과")
			return fin(OutcomeSuccess, "")
		}
	}

	// 실패 처리
	if ok, why := CanRollback(m.Schema); !ok {
		r.add(StageRollback, "skip", why)
		return fin(OutcomeHalted, fmt.Sprintf("%s 단계 실패(%v). 서비스를 멈춘 상태로 두었습니다. `harborctl restore` 로 직전 백업을 복구하세요.", failed, stepErr))
	}
	if current == "" {
		r.add(StageRollback, "skip", "신규 설치라 되돌릴 이전 버전이 없음")
		return fin(OutcomeHalted, "신규 설치가 실패했습니다. 원인을 해결하고 다시 설치하세요.")
	}
	if err := ex.Switch(ctx, TargetPrevious); err != nil {
		r.add(StageRollback, "fail", err.Error())
		return fin(OutcomeHalted, "이전 버전으로 되돌리지 못했습니다. `harborctl restore` 로 직전 백업을 복구하세요.")
	}
	if err := ex.Health(ctx, TargetPrevious); err != nil {
		r.add(StageRollback, "fail", "이전 버전 상태 점검 실패: "+err.Error())
		return fin(OutcomeHalted, "이전 버전이 상태 점검을 통과하지 못했습니다. `harborctl restore` 로 직전 백업을 복구하세요.")
	}
	r.add(StageRollback, "ok", fmt.Sprintf("이전 버전으로 복귀, 상태 점검 통과 (%s)", current))
	return fin(OutcomeRolledBack, fmt.Sprintf("%s 단계에서 실패해 %s 로 자동 복귀했습니다.", failed, current))
}

// Injection 은 시뮬레이션에서 일부러 넣는 실패다.
type Injection struct {
	BadSignature   bool  `json:"badSignature"`
	BadChecksum    bool  `json:"badChecksum"`
	FailAt         Stage `json:"failAt"`         // "" 이면 실패 없음. backup/load/migrate/switch/health
	RollbackBroken bool  `json:"rollbackBroken"` // 이전 버전 복귀 중에도 실패
}

// SimExecutor 는 Injection 대로 실패하는 가짜 실행기다. 단위 테스트와 브라우저 데모가 쓴다.
type SimExecutor struct {
	In    Injection
	Calls []string
}

var errInjected = errors.New("주입된 실패")

func (s *SimExecutor) Verify(context.Context) error {
	s.Calls = append(s.Calls, "verify")
	if s.In.BadSignature {
		return fmt.Errorf("%w (서명 불일치로 주입)", ErrSignature)
	}
	if s.In.BadChecksum {
		return fmt.Errorf("%w (이미지 파일 변조로 주입)", ErrChecksum)
	}
	return nil
}
func (s *SimExecutor) fail(st Stage) error {
	s.Calls = append(s.Calls, string(st))
	if s.In.FailAt == st {
		return fmt.Errorf("%w: %s", errInjected, st)
	}
	return nil
}
func (s *SimExecutor) Backup(context.Context) error     { return s.fail(StageBackup) }
func (s *SimExecutor) LoadImages(context.Context) error { return s.fail(StageLoad) }
func (s *SimExecutor) Migrate(context.Context) error    { return s.fail(StageMigrate) }
func (s *SimExecutor) Switch(_ context.Context, t Target) error {
	if t == TargetPrevious {
		s.Calls = append(s.Calls, "switch-previous")
		if s.In.RollbackBroken {
			return fmt.Errorf("%w: 이전 버전 복귀", errInjected)
		}
		return nil
	}
	return s.fail(StageSwitch)
}
func (s *SimExecutor) Health(_ context.Context, t Target) error {
	if t == TargetPrevious {
		s.Calls = append(s.Calls, "health-previous")
		return nil
	}
	return s.fail(StageHealth)
}
