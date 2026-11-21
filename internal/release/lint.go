package release

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 마이그레이션 단계 표기.
const (
	PhaseExpand   = "expand"   // 구버전과 호환되는 추가만
	PhaseContract = "contract" // 이전 릴리스가 이미 안 쓰는 것을 정리
	PhaseBreaking = "breaking" // 호환되지 않는 변경(자동 롤백 불가로 분류됨)
)

// Migration 은 헤더 주석이 파싱된 마이그레이션 파일이다.
//
//	-- release: v1.2.0
//	-- phase: expand
//	-- contract-of: 0003_add_state.sql     (contract 에서만)
//	-- unused-since: v1.3.0                (contract 에서만: 이 버전부터 앱이 쓰지 않음)
type Migration struct {
	File        string
	Release     string
	Phase       string
	ContractOf  string
	UnusedSince string
	SQL         string
}

var headerRe = regexp.MustCompile(`(?m)^--\s*([a-z-]+):\s*(\S+)\s*$`)

// ParseMigration 은 파일 이름과 SQL 에서 헤더를 읽는다.
func ParseMigration(file, sql string) (Migration, error) {
	m := Migration{File: file, SQL: sql}
	for _, h := range headerRe.FindAllStringSubmatch(sql, -1) {
		switch h[1] {
		case "release":
			m.Release = h[2]
		case "phase":
			m.Phase = h[2]
		case "contract-of":
			m.ContractOf = h[2]
		case "unused-since":
			m.UnusedSince = h[2]
		}
	}
	if m.Release == "" || m.Phase == "" {
		return m, fmt.Errorf("%s: release/phase 헤더가 필요함", file)
	}
	if _, err := ParseVersion(m.Release); err != nil {
		return m, fmt.Errorf("%s: release: %w", file, err)
	}
	switch m.Phase {
	case PhaseExpand, PhaseContract, PhaseBreaking:
	default:
		return m, fmt.Errorf("%s: phase 는 expand|contract|breaking: %q", file, m.Phase)
	}
	return m, nil
}

// Violation 은 규약 위반 한 건이다.
type Violation struct {
	File string `json:"file"`
	Rule string `json:"rule"`
	Msg  string `json:"msg"`
}

func (v Violation) String() string { return fmt.Sprintf("%s [%s] %s", v.File, v.Rule, v.Msg) }

var (
	commentRe  = regexp.MustCompile(`--[^\n]*`)
	spaceRe    = regexp.MustCompile(`\s+`)
	stmtSplit  = regexp.MustCompile(`;`)
	notNullRe  = regexp.MustCompile(`\bNOT NULL\b`)
	defaultRe  = regexp.MustCompile(`\bDEFAULT\b`)
	addColRe   = regexp.MustCompile(`\bADD COLUMN\b`)
	destructRe = []struct {
		rule string
		re   *regexp.Regexp
	}{
		{"drop-table", regexp.MustCompile(`\bDROP TABLE\b`)},
		{"drop-column", regexp.MustCompile(`\bDROP COLUMN\b`)},
		{"drop-constraint", regexp.MustCompile(`\bDROP CONSTRAINT\b`)},
		{"rename", regexp.MustCompile(`\bRENAME\b`)},
		{"alter-type", regexp.MustCompile(`\bALTER COLUMN\b.*\bTYPE\b`)},
		{"set-not-null", regexp.MustCompile(`\bSET NOT NULL\b`)},
		{"truncate", regexp.MustCompile(`\bTRUNCATE\b`)},
		{"delete", regexp.MustCompile(`\bDELETE FROM\b`)},
	}
)

// statements 는 주석을 지우고 대문자·공백 정규화한 문장 목록을 돌려준다.
func statements(sql string) []string {
	s := commentRe.ReplaceAllString(sql, "")
	var out []string
	for _, p := range stmtSplit.Split(s, -1) {
		p = strings.ToUpper(strings.TrimSpace(spaceRe.ReplaceAllString(p, " ")))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// destructive 는 문장에서 호환성을 깨는 동작의 규칙 이름을 모은다.
func destructive(sql string) []string {
	var rules []string
	for _, st := range statements(sql) {
		for _, d := range destructRe {
			if d.re.MatchString(st) {
				rules = append(rules, d.rule)
			}
		}
		if addColRe.MatchString(st) && notNullRe.MatchString(st) && !defaultRe.MatchString(st) {
			rules = append(rules, "add-not-null-without-default")
		}
	}
	return rules
}

// Lint 는 마이그레이션 묶음이 expand → contract 규약을 지키는지 검사한다.
func Lint(ms []Migration) []Violation {
	var vs []Violation
	byFile := map[string]Migration{}
	for _, m := range ms {
		byFile[m.File] = m
	}
	sorted := append([]Migration(nil), ms...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].File < sorted[j].File })
	var lastRel Version
	for i, m := range sorted {
		rel, _ := ParseVersion(m.Release)
		if i > 0 && rel.Compare(lastRel) < 0 {
			vs = append(vs, Violation{m.File, "order", fmt.Sprintf("릴리스 %s 가 앞 파일의 %s 보다 낮음(파일 번호 순서와 릴리스 순서가 어긋남)", m.Release, lastRel)})
		}
		lastRel = rel
		d := destructive(m.SQL)
		switch m.Phase {
		case PhaseExpand:
			for _, r := range d {
				vs = append(vs, Violation{m.File, "expand-" + r, "expand 단계에서는 구버전 앱을 깨는 변경을 할 수 없음"})
			}
		case PhaseContract:
			src, ok := byFile[m.ContractOf]
			switch {
			case m.ContractOf == "" || !ok:
				vs = append(vs, Violation{m.File, "contract-of", "contract 는 정리 대상이 된 expand 마이그레이션을 contract-of 로 가리켜야 함"})
			case src.Phase != PhaseExpand:
				vs = append(vs, Violation{m.File, "contract-of", "contract-of 가 expand 마이그레이션이 아님: " + src.File})
			default:
				sr, _ := ParseVersion(src.Release)
				if sr.Compare(rel) >= 0 {
					vs = append(vs, Violation{m.File, "contract-release", "contract 는 expand 보다 뒤 릴리스여야 함"})
				}
			}
			if m.UnusedSince == "" {
				vs = append(vs, Violation{m.File, "unused-since", "contract 는 앱이 더 쓰지 않게 된 버전(unused-since)을 밝혀야 함"})
			} else if u, err := ParseVersion(m.UnusedSince); err != nil || u.Compare(rel) >= 0 {
				vs = append(vs, Violation{m.File, "unused-since", "unused-since 는 이 릴리스보다 앞 버전이어야 함(앞 릴리스가 이미 안 써야 롤백이 안전함)"})
			}
		}
	}
	return vs
}

// Classify 는 release 에 속한 마이그레이션들이 만드는 스키마 변경 종류를 계산한다.
func Classify(ms []Migration, release string) Schema {
	s := SchemaNone
	for _, m := range ms {
		if m.Release != release {
			continue
		}
		if m.Phase == PhaseBreaking {
			return SchemaBreaking
		}
		s = SchemaCompatible
	}
	return s
}

// CheckDeclared 는 매니페스트가 선언한 schema 가 계산값과 같은지, 규약 위반이 없는지 본다.
// 위반이 있거나 선언이 다르면 빌드 단계에서 번들을 만들지 않는다.
func CheckDeclared(declared Schema, ms []Migration, release string) []Violation {
	vs := Lint(ms)
	if got := Classify(ms, release); got != declared {
		vs = append(vs, Violation{"manifest", "schema-declared", fmt.Sprintf("매니페스트는 %q 라고 선언했지만 마이그레이션은 %q 로 분류됨", declared, got)})
	}
	return vs
}
