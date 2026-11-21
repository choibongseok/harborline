// Package release 는 설치형 릴리스의 판정 로직만 담는다.
// 파일·네트워크·도커를 직접 만지지 않는다(실행은 Executor 가 맡는다). 같은 코드가 harborctl 과 브라우저(WASM)에서 돈다.
package release

import (
	"fmt"
	"strconv"
	"strings"
)

// Version 은 vMAJOR.MINOR.PATCH 형식의 버전이다.
type Version struct{ Major, Minor, Patch int }

// ParseVersion 은 "v1.2.3" 또는 "1.2.3" 을 읽는다.
func ParseVersion(s string) (Version, error) {
	t := strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(t, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("버전 형식이 vX.Y.Z 가 아님: %q", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || p == "" {
			return Version{}, fmt.Errorf("버전 형식이 vX.Y.Z 가 아님: %q", s)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare 는 v<o 이면 -1, 같으면 0, v>o 이면 1.
func (v Version) Compare(o Version) int {
	a, b := [3]int{v.Major, v.Minor, v.Patch}, [3]int{o.Major, o.Minor, o.Patch}
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
