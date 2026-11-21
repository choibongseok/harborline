// Package sim 은 브라우저 데모가 부르는 진입점이다. 판정은 internal/release 의 코드를 그대로 쓰고,
// 여기서는 JSON 입출력과 가짜 실행기(SimExecutor)만 연결한다.
package sim

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/choibongseok/harborline/internal/release"
)

// UpgradeRequest 는 화면에서 고른 시나리오다.
type UpgradeRequest struct {
	Current  string            `json:"current"` // 빈 문자열이면 신규 설치
	Manifest release.Manifest  `json:"manifest"`
	Inject   release.Injection `json:"inject"`
}

// UpgradeResponse 는 타임라인과 결정이다.
type UpgradeResponse struct {
	release.Result
	Gate         release.GateResult `json:"gate"`
	RollbackOK   bool               `json:"rollbackOk"`
	RollbackWhy  string             `json:"rollbackWhy"`
	Version      string             `json:"version"`
	Schema       release.Schema     `json:"schema"`
	CurrentAfter string             `json:"currentAfter"`
}

// Upgrade 는 JSON 요청을 받아 JSON 결과를 돌려준다.
func Upgrade(reqJSON string) (string, error) {
	var req UpgradeRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		return "", fmt.Errorf("요청 파싱: %w", err)
	}
	if _, err := release.ParseVersion(req.Manifest.Version); err != nil {
		return "", err
	}
	ex := &release.SimExecutor{In: req.Inject}
	res := release.Upgrade(context.Background(), req.Current, req.Manifest, ex)
	ok, why := release.CanRollback(req.Manifest.Schema)
	after := req.Current
	if res.Outcome == release.OutcomeSuccess {
		after = req.Manifest.Version
	}
	out := UpgradeResponse{Result: res, Gate: release.Gate(req.Current, req.Manifest), RollbackOK: ok, RollbackWhy: why,
		Version: req.Manifest.Version, Schema: req.Manifest.Schema, CurrentAfter: after}
	if out.Events == nil {
		out.Events = []release.Event{}
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// LintFile 은 검사할 SQL 파일이다.
type LintFile struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

// LintRequest 는 검사 요청이다. Release/Declared 가 있으면 선언한 schema 와 계산값도 대조한다.
type LintRequest struct {
	Files    []LintFile     `json:"files"`
	Release  string         `json:"release"`
	Declared release.Schema `json:"declared"`
}

// LintResponse 는 위반 목록과 분류 결과다.
type LintResponse struct {
	Violations []release.Violation `json:"violations"`
	Classified release.Schema      `json:"classified"`
	ParseError string              `json:"parseError,omitempty"`
}

// Lint 는 마이그레이션 규약 검사기를 돌린다.
func Lint(reqJSON string) (string, error) {
	var req LintRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		return "", fmt.Errorf("요청 파싱: %w", err)
	}
	var ms []release.Migration
	resp := LintResponse{Violations: []release.Violation{}}
	for _, f := range req.Files {
		m, err := release.ParseMigration(f.Name, f.SQL)
		if err != nil {
			resp.ParseError = err.Error()
			b, _ := json.Marshal(resp)
			return string(b), nil
		}
		ms = append(ms, m)
	}
	if req.Release != "" && req.Declared != "" {
		resp.Violations = release.CheckDeclared(req.Declared, ms, req.Release)
		resp.Classified = release.Classify(ms, req.Release)
	} else {
		resp.Violations = release.Lint(ms)
	}
	if resp.Violations == nil {
		resp.Violations = []release.Violation{}
	}
	b, err := json.Marshal(resp)
	return string(b), err
}
