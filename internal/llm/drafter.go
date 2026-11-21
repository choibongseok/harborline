package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 처리 경로 이름.
const (
	PathTools      = "tools"       // 도구 호출(네이티브)
	PathJSONMode   = "json_mode"   // JSON 모드(대체)
	PathPromptJSON = "prompt_json" // 프롬프트로 JSON 요구 + 관대한 파싱(대체)
	PathDirect     = "direct"      // 한 번에 처리(네이티브)
	PathChunked    = "chunked"     // 나눠서 요약 후 처리(대체)
	PathDisabled   = "disabled"    // 기능 끔
)

// ErrDisabled 는 탐지 결과 기능을 쓸 수 없을 때다.
var ErrDisabled = errors.New("LLM 기능이 꺼져 있음")

// Classification 은 문의 분류 결과다.
type Classification struct {
	Category string `json:"category"`
	Priority int    `json:"priority"`
	Path     string `json:"path"`
}

// Draft 는 답변 초안이다.
type Draft struct {
	Text string `json:"text"`
	Path string `json:"path"`
}

// Drafter 는 탐지 결과(Caps)에 맞는 경로로 분류·초안 기능을 제공한다.
type Drafter struct {
	C    *Client
	Caps Caps
}

// NewDrafter 는 엔드포인트를 탐지해 Drafter 를 만든다.
func NewDrafter(ctx context.Context, c *Client) *Drafter {
	return &Drafter{C: c, Caps: c.Probe(ctx)}
}

// ClassifyPath 는 탐지 결과에 따른 분류 경로다.
func (d *Drafter) ClassifyPath() string {
	switch {
	case !d.Caps.Reachable:
		return PathDisabled
	case d.Caps.Tools:
		return PathTools
	case d.Caps.JSONMode:
		return PathJSONMode
	default:
		return PathPromptJSON
	}
}

// budget 은 한 번에 보낼 입력 토큰 예산(컨텍스트의 절반)이다.
func (d *Drafter) budget() int { return d.Caps.ContextTokens / 2 }

// DraftPath 는 입력 길이에 따른 초안 경로다.
func (d *Drafter) DraftPath(thread string) string {
	switch {
	case !d.Caps.Reachable || d.budget() < 64:
		return PathDisabled
	case EstimateTokens(thread) <= d.budget():
		return PathDirect
	default:
		return PathChunked
	}
}

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// ParseLooseJSON 은 코드 펜스나 앞뒤 설명이 붙은 응답에서 JSON 객체를 꺼낸다.
func ParseLooseJSON(s string, v any) error {
	s = strings.TrimSpace(s)
	if json.Unmarshal([]byte(s), v) == nil {
		return nil
	}
	if m := fenceRe.FindStringSubmatch(s); m != nil && json.Unmarshal([]byte(strings.TrimSpace(m[1])), v) == nil {
		return nil
	}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i && json.Unmarshal([]byte(s[i:j+1]), v) == nil {
		return nil
	}
	return errors.New("응답에서 JSON 을 찾지 못함")
}

type clsOut struct {
	Category string `json:"category"`
	Priority int    `json:"priority"`
}

func validCategory(c string) bool {
	for _, k := range categories {
		if k == c {
			return true
		}
	}
	return false
}

// Classify 는 문의 본문을 분류한다. 경로는 탐지 결과로 정해진다.
func (d *Drafter) Classify(ctx context.Context, text string) (Classification, error) {
	path := d.ClassifyPath()
	if path == PathDisabled {
		return Classification{Path: path}, ErrDisabled
	}
	if max := d.budget(); max > 0 && EstimateTokens(text) > max {
		text = text[:max*4] // 분류는 앞부분이면 충분하다
	}
	prompt := "Classify this support inquiry into category (billing, account, bug, general) and priority (0 normal, 1 high, 2 urgent).\n" + text
	var out clsOut
	switch path {
	case PathTools:
		r, err := d.C.chat(ctx, chatRequest{
			Messages: []Message{{"user", prompt}}, Tools: []tool{classifyTool()},
			ToolChoice: map[string]any{"type": "function", "function": map[string]string{"name": "record_classification"}}, MaxTokens: 64,
		})
		if err != nil {
			return Classification{Path: path}, err
		}
		if len(r.Choices[0].Message.ToolCalls) == 0 {
			return Classification{Path: path}, errors.New("tool_calls 없음")
		}
		if err := json.Unmarshal([]byte(r.Choices[0].Message.ToolCalls[0].Function.Arguments), &out); err != nil {
			return Classification{Path: path}, err
		}
	case PathJSONMode, PathPromptJSON:
		req := chatRequest{Messages: []Message{{"user", prompt + "\nRespond with JSON only: {\"category\": \"...\", \"priority\": 0}"}}, MaxTokens: 64}
		if path == PathJSONMode {
			req.ResponseFormat = map[string]string{"type": "json_object"}
		}
		r, err := d.C.chat(ctx, req)
		if err != nil {
			return Classification{Path: path}, err
		}
		if err := ParseLooseJSON(r.Choices[0].Message.Content, &out); err != nil {
			return Classification{Path: path}, err
		}
	}
	if !validCategory(out.Category) {
		return Classification{Path: path}, fmt.Errorf("허용되지 않은 분류: %q", out.Category)
	}
	if out.Priority < 0 || out.Priority > 2 {
		out.Priority = 0
	}
	return Classification{Category: out.Category, Priority: out.Priority, Path: path}, nil
}

// chunk 는 긴 글을 줄 경계에서 size 글자 이하 조각으로 나눈다.
func chunk(s string, size int) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		for len(line) > size {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			out = append(out, line[:size])
			line = line[size:]
		}
		if cur.Len()+len(line)+1 > size && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteString(line + "\n")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// DraftReply 는 스레드(문의 + 이전 대화)를 보고 답변 초안을 만든다.
// 입력이 예산을 넘으면 마지막 메시지는 그대로 두고 앞부분을 조각별로 요약해 줄인다.
func (d *Drafter) DraftReply(ctx context.Context, history, last string) (Draft, error) {
	full := history + "\n" + last
	path := d.DraftPath(full)
	if path == PathDisabled {
		return Draft{Path: path}, ErrDisabled
	}
	ask := func(context string) (string, error) {
		return d.C.Complete(ctx, []Message{{"user", "Draft a reply to the customer's latest message.\nContext:\n" + context + "\nLatest message:\n" + last}}, 200)
	}
	if path == PathDirect {
		t, err := ask(history)
		return Draft{Text: t, Path: path}, err
	}
	ctxText := history
	size := d.budget() * 4 * 8 / 10 // 조각 하나가 예산의 80% 를 넘지 않게
	for depth := 0; depth < 3 && EstimateTokens(ctxText+last) > d.budget(); depth++ {
		var sums []string
		for _, ch := range chunk(ctxText, size) {
			s, err := d.C.Complete(ctx, []Message{{"user", "Summarize:\n" + ch}}, 80)
			if err != nil {
				return Draft{Path: path}, err
			}
			sums = append(sums, strings.TrimSpace(s))
		}
		ctxText = strings.Join(sums, "\n")
	}
	if EstimateTokens(ctxText+last) > d.budget() {
		return Draft{Path: PathDisabled}, fmt.Errorf("%w: 요약해도 입력이 컨텍스트에 들어가지 않음", ErrDisabled)
	}
	t, err := ask(ctxText)
	return Draft{Text: t, Path: path}, err
}
