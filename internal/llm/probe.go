package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Caps 는 탐지한 엔드포인트 기능이다.
type Caps struct {
	Reachable     bool     `json:"reachable"`
	Tools         bool     `json:"tools"`
	JSONMode      bool     `json:"json_mode"`
	ContextTokens int      `json:"context_tokens"` // 통과한 가장 큰 단계(하한). 0 이면 가장 작은 단계도 못 넘음
	Notes         []string `json:"notes,omitempty"`
}

// 탐지에 쓰는 컨텍스트 단계(토큰 수 추정치).
var ctxLadder = []int{256, 512, 1024, 2048, 4096, 8192, 16384}

// EstimateTokens 는 토큰 수를 글자 수로 어림한다(바이트/4). 실제 토크나이저와 다르므로 예산에 여유를 둔다.
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }

// 분류 도구 정의. 도구 호출 탐지와 실제 분류가 같은 정의를 쓴다.
var categories = []string{"billing", "account", "bug", "general"}

func classifyTool() tool {
	return tool{Type: "function", Function: toolFn{
		Name:        "record_classification",
		Description: "문의 분류를 기록한다",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{"type": "string", "enum": categories},
				"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 2},
			},
			"required": []string{"category", "priority"},
		},
	}}
}

// Probe 는 엔드포인트의 기능을 실제 요청으로 확인한다. 실패해도 서비스는 뜬다(기능만 꺼진다).
func (c *Client) Probe(ctx context.Context) Caps {
	var caps Caps
	if err := c.Models(ctx); err != nil {
		caps.Notes = append(caps.Notes, "엔드포인트에 닿지 않음: "+err.Error())
		return caps
	}
	caps.Reachable = true

	// 도구 호출: 강제 호출을 요청하고 tool_calls 가 오는지 본다.
	r, err := c.chat(ctx, chatRequest{
		Messages:   []Message{{"user", "Classify: I want a refund for my invoice"}},
		Tools:      []tool{classifyTool()},
		ToolChoice: map[string]any{"type": "function", "function": map[string]string{"name": "record_classification"}},
		MaxTokens:  64,
	})
	switch {
	case err != nil:
		caps.Notes = append(caps.Notes, "도구 호출 미지원: "+err.Error())
	case len(r.Choices[0].Message.ToolCalls) == 0:
		caps.Notes = append(caps.Notes, "도구 호출 미지원: 응답에 tool_calls 없음")
	default:
		var args struct{ Category string }
		tc := r.Choices[0].Message.ToolCalls[0].Function
		if tc.Name == "record_classification" && json.Unmarshal([]byte(tc.Arguments), &args) == nil && args.Category != "" {
			caps.Tools = true
		} else {
			caps.Notes = append(caps.Notes, "도구 호출 응답이 형식에 맞지 않음")
		}
	}

	// JSON 출력: response_format 을 요청하고, 응답 전체가 JSON 인지 본다(무시하는 서버가 많아 요청 성공만으로는 판단하지 않는다).
	r, err = c.chat(ctx, chatRequest{
		Messages:       []Message{{"user", `Reply with a JSON object {"ok": true} and nothing else.`}},
		ResponseFormat: map[string]string{"type": "json_object"},
		MaxTokens:      32,
	})
	switch {
	case err != nil:
		caps.Notes = append(caps.Notes, "JSON 모드 미지원: "+err.Error())
	default:
		var v map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(r.Choices[0].Message.Content)), &v) == nil {
			caps.JSONMode = true
		} else {
			caps.Notes = append(caps.Notes, "JSON 모드를 요청했지만 응답이 순수 JSON 이 아님(무시한 것으로 판단)")
		}
	}

	// 컨텍스트 길이: 단계별로 채운 입력을 보내 본다.
	for _, rung := range ctxLadder {
		filler := strings.Repeat("a", rung*4*9/10)
		_, err := c.chat(ctx, chatRequest{Messages: []Message{{"user", "Count the letters: " + filler}}, MaxTokens: 1})
		if err == nil {
			caps.ContextTokens = rung
			continue
		}
		if !errors.Is(err, ErrContext) {
			caps.Notes = append(caps.Notes, "컨텍스트 탐지 중 오류: "+err.Error())
		}
		break
	}
	return caps
}
