package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/choibongseok/harborline/internal/llm/mock"
)

func serve(t *testing.T, profile string) *Client {
	t.Helper()
	p, ok := mock.Lookup(profile)
	if !ok {
		t.Fatalf("프로필 없음: %s", profile)
	}
	srv := httptest.NewServer(&mock.Server{P: p})
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}
}

func TestProbeFullProfileDetectsAllFeatures(t *testing.T) {
	caps := serve(t, "full").Probe(context.Background())
	if !caps.Reachable || !caps.Tools || !caps.JSONMode || caps.ContextTokens != 8192 {
		t.Fatalf("%+v", caps)
	}
}

func TestProbeDetectsMissingToolCalling(t *testing.T) {
	caps := serve(t, "no-tools").Probe(context.Background())
	if caps.Tools || !caps.JSONMode {
		t.Fatalf("도구 호출만 없어야 함: %+v", caps)
	}
}

func TestProbeDetectsIgnoredJSONMode(t *testing.T) {
	caps := serve(t, "no-json").Probe(context.Background())
	if caps.JSONMode {
		t.Fatalf("response_format 을 무시하고 문장을 돌려주면 JSON 모드 미지원으로 봐야 함: %+v", caps)
	}
}

func TestProbeMeasuresShortContext(t *testing.T) {
	caps := serve(t, "short-ctx").Probe(context.Background())
	if caps.ContextTokens != 1024 || !caps.Tools {
		t.Fatalf("%+v", caps)
	}
}

func TestProbeUnreachableEndpointDisablesInsteadOfFailing(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Model: "m", HTTP: http.DefaultClient}
	caps := c.Probe(context.Background())
	if caps.Reachable || len(caps.Notes) == 0 {
		t.Fatalf("%+v", caps)
	}
}

func TestParseLooseJSONHandlesFencesAndProse(t *testing.T) {
	var v struct{ Category string }
	for _, in := range []string{`{"category":"bug"}`, "```json\n{\"category\":\"bug\"}\n```", "Sure! {\"category\":\"bug\"} thanks"} {
		v.Category = ""
		if err := ParseLooseJSON(in, &v); err != nil || v.Category != "bug" {
			t.Errorf("%q: %v %v", in, v, err)
		}
	}
	if err := ParseLooseJSON("no json here", &v); err == nil {
		t.Fatal("JSON 이 없으면 오류")
	}
}

func classifyOn(t *testing.T, profile string) (Classification, error) {
	d := NewDrafter(context.Background(), serve(t, profile))
	return d.Classify(context.Background(), "로그인이 안 됩니다. 긴급합니다 password reset")
}

func TestClassifyUsesToolsWhenSupported(t *testing.T) {
	c, err := classifyOn(t, "full")
	if err != nil || c.Path != PathTools || c.Category != "account" || c.Priority != 2 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestClassifyFallsBackToJSONMode(t *testing.T) {
	c, err := classifyOn(t, "no-tools")
	if err != nil || c.Path != PathJSONMode || c.Category != "account" || c.Priority != 2 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestClassifyFallsBackToPromptJSONWithFencedReply(t *testing.T) {
	c, err := classifyOn(t, "no-json")
	if err != nil || c.Path != PathPromptJSON || c.Category != "account" || c.Priority != 2 {
		t.Fatalf("코드 펜스가 붙은 응답도 읽어야 함: %+v %v", c, err)
	}
}

func TestClassifyDisabledWhenEndpointDown(t *testing.T) {
	d := NewDrafter(context.Background(), &Client{BaseURL: "http://127.0.0.1:1", HTTP: http.DefaultClient})
	if _, err := d.Classify(context.Background(), "x"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("%v", err)
	}
}

func longThread() (string, string) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("Customer: 이전에 문의드린 건입니다 번호 " + strings.Repeat("x", 80) + "\nAgent: 확인 중입니다 " + strings.Repeat("y", 80) + "\n")
	}
	return b.String(), "환불 refund 요청드립니다"
}

func TestDraftDirectWhenThreadFitsContext(t *testing.T) {
	d := NewDrafter(context.Background(), serve(t, "full"))
	h, last := longThread()
	dr, err := d.DraftReply(context.Background(), h, last)
	if err != nil || dr.Path != PathDirect || !strings.Contains(dr.Text, "DRAFT(billing)") {
		t.Fatalf("%+v %v", dr, err)
	}
}

func TestDraftChunksLongThreadOnShortContext(t *testing.T) {
	d := NewDrafter(context.Background(), serve(t, "short-ctx"))
	h, last := longThread()
	dr, err := d.DraftReply(context.Background(), h, last)
	if err != nil || dr.Path != PathChunked || !strings.Contains(dr.Text, "DRAFT(billing)") {
		t.Fatalf("마지막 메시지는 보존한 채 앞부분만 요약해 초안이 나와야 함: %+v %v", dr, err)
	}
}

func TestDraftDisabledWhenContextTooSmallForAnything(t *testing.T) {
	d := &Drafter{C: serve(t, "full"), Caps: Caps{Reachable: true, ContextTokens: 64}}
	if _, err := d.DraftReply(context.Background(), "a", "b"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("%v", err)
	}
}
