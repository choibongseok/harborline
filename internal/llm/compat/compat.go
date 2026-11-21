// Package compat 는 같은 시나리오를 모델 프로필마다 돌려 통과/대체/비활성 표를 만든다(호환성 검증).
package compat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/choibongseok/harborline/internal/llm"
	"github.com/choibongseok/harborline/internal/llm/mock"
)

// 결과 값.
const (
	Pass     = "통과"
	Fallback = "대체 경로"
	Disabled = "비활성"
	Failed   = "실패"
)

// Cell 은 시나리오 하나의 결과다.
type Cell struct {
	Status string `json:"status"`
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
}

// Row 는 프로필 하나의 결과다.
type Row struct {
	Profile       string `json:"profile"`
	Tools         bool   `json:"tools"`
	JSONMode      bool   `json:"json_mode"`
	ContextTokens int    `json:"context_tokens"`
	Reachable     bool   `json:"reachable"`
	Classify      Cell   `json:"classify"`
	DraftShort    Cell   `json:"draft_short"`
	DraftLong     Cell   `json:"draft_long"`
}

const inquiry = "로그인이 안 됩니다. 긴급합니다 password reset"

func longThread() (string, string) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("Customer: 이전에 문의드린 건입니다 번호 " + strings.Repeat("x", 80) + "\nAgent: 확인 중입니다 " + strings.Repeat("y", 80) + "\n")
	}
	return b.String(), "환불 refund 요청드립니다"
}

func status(path string, err error, ok bool, native ...string) Cell {
	switch {
	case errors.Is(err, llm.ErrDisabled):
		return Cell{Status: Disabled, Path: llm.PathDisabled}
	case err != nil:
		return Cell{Status: Failed, Path: path, Detail: err.Error()}
	case !ok:
		return Cell{Status: Failed, Path: path, Detail: "결과가 기대와 다름"}
	}
	for _, n := range native {
		if path == n {
			return Cell{Status: Pass, Path: path}
		}
	}
	return Cell{Status: Fallback, Path: path}
}

// RunProfile 은 엔드포인트 하나를 탐지하고 세 시나리오를 돌린다.
func RunProfile(ctx context.Context, name string, c *llm.Client) Row {
	d := llm.NewDrafter(ctx, c)
	r := Row{Profile: name, Tools: d.Caps.Tools, JSONMode: d.Caps.JSONMode, ContextTokens: d.Caps.ContextTokens, Reachable: d.Caps.Reachable}

	cls, err := d.Classify(ctx, inquiry)
	r.Classify = status(cls.Path, err, cls.Category == "account" && cls.Priority == 2, llm.PathTools)

	short, err := d.DraftReply(ctx, "Customer: "+inquiry+"\n", inquiry)
	r.DraftShort = status(short.Path, err, strings.Contains(short.Text, "DRAFT(account)"), llm.PathDirect)

	h, last := longThread()
	long, err := d.DraftReply(ctx, h, last)
	r.DraftLong = status(long.Path, err, strings.Contains(long.Text, "DRAFT(billing)"), llm.PathDirect)
	return r
}

// Run 은 모의 서버의 모든 프로필과 응답 없는 엔드포인트를 검증한다.
func Run(ctx context.Context) []Row {
	var rows []Row
	for _, p := range mock.Profiles {
		srv := httptest.NewServer(&mock.Server{P: p})
		rows = append(rows, RunProfile(ctx, p.Name, &llm.Client{BaseURL: srv.URL, Model: "m", HTTP: srv.Client()}))
		srv.Close()
	}
	down := &llm.Client{BaseURL: "http://127.0.0.1:1", Model: "m", HTTP: &http.Client{Timeout: 2 * time.Second}}
	rows = append(rows, RunProfile(ctx, "unreachable", down))
	return rows
}

func mark(b bool) string {
	if b {
		return "지원"
	}
	return "미지원"
}

func cell(c Cell) string {
	if c.Status == Disabled || c.Path == "" {
		return c.Status
	}
	return fmt.Sprintf("%s (%s)", c.Status, c.Path)
}

// Markdown 은 표를 마크다운으로 만든다.
func Markdown(rows []Row) string {
	var b strings.Builder
	b.WriteString("| 프로필 | 도구 호출 | JSON 모드 | 컨텍스트(토큰) | 분류(구조화 출력) | 답변 초안(짧은 스레드) | 답변 초안(긴 스레드) |\n|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		ctxs := fmt.Sprint(r.ContextTokens)
		if !r.Reachable {
			ctxs = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", r.Profile, mark(r.Tools), mark(r.JSONMode), ctxs, cell(r.Classify), cell(r.DraftShort), cell(r.DraftLong))
	}
	return b.String()
}
