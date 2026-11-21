package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// API 는 클라우드 메일 API(JSON POST)로 보낸다. 클라우드 프로필에서만 쓴다.
type API struct {
	URL    string
	Token  string
	Client *http.Client
}

func (a *API) Kind() string { return "api" }

func (a *API) Send(ctx context.Context, m Message) error {
	body, _ := json.Marshal(map[string]string{"from": m.From, "to": m.To, "subject": m.Subject, "text": m.Body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return fmt.Errorf("메일 API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("메일 API %d: %s", resp.StatusCode, b)
	}
	return nil
}
