package ctl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"text/template"
)

func q(s string) string { b, _ := json.Marshal(s); return string(b) }

var composeTpl = template.Must(template.New("c").Funcs(template.FuncMap{"q": q}).Parse(`# harborctl 이 만든 파일입니다. 직접 고치지 마세요(설정은 install.json).
name: {{.Project}}
services:
  db:
    image: postgres:16-alpine
    container_name: {{.Project}}-db
    environment:
      POSTGRES_USER: harborline
      POSTGRES_PASSWORD: {{q .PGPassword}}
      POSTGRES_DB: harborline
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U harborline -d harborline"]
      interval: 2s
      timeout: 3s
      retries: 30
    networks: [site]
    restart: unless-stopped
  app:
    image: {{.AppImage}}
    container_name: {{.Project}}-app
    depends_on:
      db:
        condition: service_healthy
    read_only: true
    environment:
{{- range .Env}}
      {{.K}}: {{q .V}}
{{- end}}
    volumes:
      - {{q (printf "%s:/data/attachments" .AttachDir)}}
{{- range .Mounts}}
      - {{q (printf "%s:%s:ro" .Host .Container)}}
{{- end}}
{{- if .DNS}}
    dns:
{{- range .DNS}}
      - {{.}}
{{- end}}
{{- end}}
{{- if .Ports}}
    ports:
{{- range .Ports}}
      - {{q .}}
{{- end}}
{{- end}}
    networks: [site]
    restart: unless-stopped
volumes:
  pgdata: {}
networks:
  site:
    name: {{.Network.Name}}
{{- if .Network.External}}
    external: true
{{- else if .Network.Internal}}
    internal: true
{{- end}}
`))

type kv struct{ K, V string }

type composeData struct {
	Config
	AppImage  string
	AttachDir string
	Env       []kv
}

// appEnv 는 앱 컨테이너 환경 변수다. 설정 파일의 env 가 기본값을 덮는다.
func appEnv(c Config) []kv {
	m := map[string]string{
		"HARBORLINE_PROFILE":        "onprem",
		"HARBORLINE_LISTEN":         ":8080",
		"HARBORLINE_DATABASE_URL":   fmt.Sprintf("postgres://harborline:%s@%s-db:5432/harborline?sslmode=disable", c.PGPassword, c.Project),
		"HARBORLINE_SESSION_SECRET": c.SessionSecret,
		"HARBORLINE_STORAGE_DIR":    "/data/attachments",
	}
	for k, v := range c.Env {
		m[k] = v
	}
	var out []kv
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].K < out[j].K })
	return out
}

// RenderCompose 는 설정과 대상 버전으로 compose 파일을 만든다.
func RenderCompose(c Config, version, attachDir string) ([]byte, error) {
	var buf bytes.Buffer
	err := composeTpl.Execute(&buf, composeData{Config: c, AppImage: "harborline-app:" + version, AttachDir: attachDir, Env: appEnv(c)})
	return buf.Bytes(), err
}
