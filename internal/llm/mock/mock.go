// Package mock 은 고객사 내부 OpenAI 호환 모델 서버의 모의 구현이다.
// 규칙 기반이라 모델 품질을 대신하지 않는다. 기능이 다른 프로필을 흉내 내서 어댑터의 기능 탐지와 대체 경로만 시험한다.
package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

// Profile 은 모의 서버가 지원하는 기능 조합이다.
type Profile struct {
	Name     string
	Tools    bool
	JSONMode bool
	Ctx      int // 컨텍스트 토큰 수(글자/4 로 센다)
}

// Profiles 는 제공하는 프로필이다.
var Profiles = []Profile{
	{"full", true, true, 8192},
	{"no-tools", false, true, 8192},
	{"no-json", false, false, 8192},
	{"short-ctx", true, true, 1024},
	{"bare", false, false, 1024},
}

// Lookup 은 이름으로 프로필을 찾는다.
func Lookup(name string) (Profile, bool) {
	for _, p := range Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Server 는 요청 수를 세는 모의 서버 핸들러다.
type Server struct {
	P        Profile
	Requests atomic.Int64
}

type msg struct{ Role, Content string }
type reqBody struct {
	Messages       []msg             `json:"messages"`
	Tools          []json.RawMessage `json:"tools"`
	ResponseFormat map[string]string `json:"response_format"`
}

// inquiryText 는 어댑터가 붙인 지시문을 떼고 고객 문장만 남긴다.
func inquiryText(s string) string {
	if strings.HasPrefix(s, "Classify this support inquiry") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.Index(s, "\nRespond with JSON only"); j >= 0 {
			s = s[:j]
		}
	}
	return s
}

func classify(text string) (string, int) {
	l := strings.ToLower(inquiryText(text))
	cat := "general"
	switch {
	case strings.Contains(l, "refund") || strings.Contains(l, "invoice") || strings.Contains(l, "환불"):
		cat = "billing"
	case strings.Contains(l, "password") || strings.Contains(l, "login") || strings.Contains(l, "로그인"):
		cat = "account"
	case strings.Contains(l, "crash") || strings.Contains(l, "error") || strings.Contains(l, "오류"):
		cat = "bug"
	}
	pr := 0
	if strings.Contains(l, "urgent") || strings.Contains(l, "긴급") {
		pr = 2
	}
	return cat, pr
}

func errJSON(w http.ResponseWriter, code int, c, m string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": c, "message": m}})
}

// ServeHTTP 는 /v1/models 와 /v1/chat/completions 를 처리한다.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/models":
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"internal-model","object":"model"}]}`))
	case "/v1/chat/completions":
		s.chat(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var rb reqBody
	if err := json.NewDecoder(r.Body).Decode(&rb); err != nil {
		errJSON(w, 400, "bad_request", err.Error())
		return
	}
	total := 0
	var last string
	for _, m := range rb.Messages {
		total += len(m.Content)
		last = m.Content
	}
	if (total+3)/4 > s.P.Ctx {
		errJSON(w, 400, "context_length_exceeded", fmt.Sprintf("This model's maximum context length is %d tokens", s.P.Ctx))
		return
	}
	reply := func(content string, toolArgs string) {
		m := map[string]any{"role": "assistant", "content": content}
		if toolArgs != "" {
			m["content"] = nil
			m["tool_calls"] = []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]string{"name": "record_classification", "arguments": toolArgs}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": m}}})
	}
	if len(rb.Tools) > 0 {
		if !s.P.Tools {
			errJSON(w, 400, "unsupported", "tools are not supported by this model")
			return
		}
		cat, pr := classify(last)
		a, _ := json.Marshal(map[string]any{"category": cat, "priority": pr})
		reply("", string(a))
		return
	}
	switch {
	case strings.HasPrefix(last, "Reply with a JSON object"):
		if s.P.JSONMode && rb.ResponseFormat["type"] == "json_object" {
			reply(`{"ok": true}`, "")
		} else {
			reply("Sure! Here is the JSON: {\"ok\": true}", "") // response_format 을 무시하는 서버
		}
	case strings.Contains(last, "Respond with JSON only"):
		cat, pr := classify(last)
		body := fmt.Sprintf(`{"category": %q, "priority": %d}`, cat, pr)
		if s.P.JSONMode && rb.ResponseFormat["type"] == "json_object" {
			reply(body, "")
		} else {
			reply("Here is the classification:\n```json\n"+body+"\n```\nLet me know if you need more.", "")
		}
	case strings.HasPrefix(last, "Summarize:"):
		t := strings.TrimSpace(strings.TrimPrefix(last, "Summarize:"))
		cat, _ := classify(t)
		if len(t) > 40 {
			t = t[:40]
		}
		reply(fmt.Sprintf("SUMMARY(%s): %s", cat, strings.ReplaceAll(t, "\n", " ")), "")
	case strings.HasPrefix(last, "Draft a reply"):
		i := strings.Index(last, "Latest message:\n")
		latest := last[i+len("Latest message:\n"):]
		cat, _ := classify(latest)
		reply(fmt.Sprintf("DRAFT(%s): 안녕하세요. 문의하신 내용을 확인했습니다. [%s]", cat, strings.TrimSpace(firstN(latest, 30))), "")
	default:
		reply("OK", "")
	}
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
