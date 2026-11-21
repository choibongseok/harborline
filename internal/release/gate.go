package release

import "fmt"

// 게이트 판정 코드.
const (
	GateOK         = "ok"
	GateSame       = "same-version"
	GateDowngrade  = "downgrade"
	GateSkipped    = "below-min-from"
	GateBadVersion = "bad-version"
)

// GateResult 는 매니페스트 게이트의 판정이다.
type GateResult struct {
	Allowed bool   `json:"allowed"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Gate 는 현재 설치 버전(빈 문자열이면 신규 설치)에서 m 으로 올릴 수 있는지 판정한다.
func Gate(current string, m Manifest) GateResult {
	target, err := ParseVersion(m.Version)
	if err != nil {
		return GateResult{false, GateBadVersion, err.Error()}
	}
	min, err := ParseVersion(m.MinFrom)
	if err != nil {
		return GateResult{false, GateBadVersion, err.Error()}
	}
	if current == "" {
		return GateResult{true, GateOK, "신규 설치"}
	}
	cur, err := ParseVersion(current)
	if err != nil {
		return GateResult{false, GateBadVersion, fmt.Sprintf("설치된 버전을 읽을 수 없음: %v", err)}
	}
	switch c := target.Compare(cur); {
	case c == 0:
		return GateResult{false, GateSame, fmt.Sprintf("이미 %s 입니다", cur)}
	case c < 0:
		return GateResult{false, GateDowngrade, fmt.Sprintf("%s 에서 %s 로 내리는 것은 업그레이드가 아님(롤백 명령 사용)", cur, target)}
	}
	if cur.Compare(min) < 0 {
		return GateResult{false, GateSkipped, fmt.Sprintf("%s 로 올리려면 먼저 %s 이상이어야 함(현재 %s, 건너뛴 버전 있음)", target, min, cur)}
	}
	return GateResult{true, GateOK, fmt.Sprintf("%s → %s 가능", cur, target)}
}

// CanRollback 은 스키마 변경 종류만 보고 자동·수동 롤백(앱만 이전 버전으로 되돌리기)이 안전한지 답한다.
func CanRollback(s Schema) (bool, string) {
	switch s {
	case SchemaNone:
		return true, "스키마 변경이 없어 이전 버전 이미지로 그대로 되돌릴 수 있음"
	case SchemaCompatible:
		return true, "expand/contract 규약을 지킨 변경이라 이전 버전 앱이 확장된 스키마에서도 동작함"
	default:
		return false, "스키마 변경이 호환되지 않음: 자동 롤백 없이 정지하고 백업 복구를 안내함"
	}
}
