// Package llm 은 OpenAI 호환 채팅 엔드포인트(호스팅 API 또는 고객사 내부 모델 서버) 어댑터다.
// 시작할 때 서버가 지원하는 기능(도구 호출·JSON 출력·컨텍스트 길이)을 탐지하고,
// 지원하지 않으면 대체 경로를 쓰거나 기능을 끈다.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Message 는 채팅 메시지다.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type toolFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}
type tool struct {
	Type     string `json:"type"`
	Function toolFn `json:"function"`
}

type chatRequest struct {
	Model          string            `json:"model"`
	Messages       []Message         `json:"messages"`
	Tools          []tool            `json:"tools,omitempty"`
	ToolChoice     any               `json:"tool_choice,omitempty"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	Temperature    float64           `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// ErrContext 는 입력이 모델의 컨텍스트 길이를 넘었을 때다.
var ErrContext = errors.New("컨텍스트 길이 초과")

// Client 는 엔드포인트 하나에 대한 저수준 클라이언트다.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func (c *Client) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, b, err
}

func (c *Client) chat(ctx context.Context, req chatRequest) (*chatResponse, error) {
	req.Model = c.Model
	status, b, err := c.do(ctx, http.MethodPost, "/v1/chat/completions", req)
	if err != nil {
		return nil, err
	}
	var r chatResponse
	_ = json.Unmarshal(b, &r)
	if status/100 != 2 {
		msg := strings.TrimSpace(string(b))
		code := ""
		if r.Error != nil {
			msg, code = r.Error.Message, r.Error.Code
		}
		if code == "context_length_exceeded" || strings.Contains(strings.ToLower(msg), "context length") {
			return nil, fmt.Errorf("%w: %s", ErrContext, msg)
		}
		return nil, fmt.Errorf("llm %d: %s", status, msg)
	}
	if len(r.Choices) == 0 {
		return nil, errors.New("llm 응답에 choices 가 없음")
	}
	return &r, nil
}

// Complete 는 일반 텍스트 응답을 받는다.
func (c *Client) Complete(ctx context.Context, msgs []Message, maxTokens int) (string, error) {
	r, err := c.chat(ctx, chatRequest{Messages: msgs, MaxTokens: maxTokens})
	if err != nil {
		return "", err
	}
	return r.Choices[0].Message.Content, nil
}

// Models 는 /v1/models 에 닿는지 본다.
func (c *Client) Models(ctx context.Context) error {
	status, b, err := c.do(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("/v1/models %d: %s", status, strings.TrimSpace(string(b)))
	}
	return nil
}
