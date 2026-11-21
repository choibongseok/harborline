package compat

import (
	"context"
	"testing"
)

type want struct{ classify, short, long string }

func TestCompatibilityMatrixMatchesExpectedStatusesForEveryProfile(t *testing.T) {
	expect := map[string]want{
		"full":        {Pass, Pass, Pass},
		"no-tools":    {Fallback, Pass, Pass},
		"no-json":     {Fallback, Pass, Pass},
		"short-ctx":   {Pass, Pass, Fallback},
		"bare":        {Fallback, Pass, Fallback},
		"unreachable": {Disabled, Disabled, Disabled},
	}
	rows := Run(context.Background())
	if len(rows) != len(expect) {
		t.Fatalf("프로필 수 %d", len(rows))
	}
	for _, r := range rows {
		w := expect[r.Profile]
		if r.Classify.Status != w.classify || r.DraftShort.Status != w.short || r.DraftLong.Status != w.long {
			t.Errorf("%s: 분류 %v / 짧은 초안 %v / 긴 초안 %v (기대 %+v)", r.Profile, r.Classify, r.DraftShort, r.DraftLong, w)
		}
	}
}

func TestCompatibilityPathsNameTheFallbackUsed(t *testing.T) {
	paths := map[string]string{}
	for _, r := range Run(context.Background()) {
		paths[r.Profile] = r.Classify.Path + "/" + r.DraftLong.Path
	}
	for p, w := range map[string]string{"full": "tools/direct", "no-tools": "json_mode/direct", "no-json": "prompt_json/direct", "short-ctx": "tools/chunked", "bare": "prompt_json/chunked"} {
		if paths[p] != w {
			t.Errorf("%s 경로 %s (기대 %s)", p, paths[p], w)
		}
	}
}
