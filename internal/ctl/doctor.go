package ctl

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Check 는 사전 점검 한 항목이다.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

// FreeGB 는 경로가 속한 파일시스템의 남은 용량(GB)이다.
func FreeGB(path string) (float64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}
	return float64(s.Bavail) * float64(s.Bsize) / (1 << 30), nil
}

// CheckCAFile 은 PEM 의 모든 인증서가 읽히고 만료까지 30일 넘게 남았는지 본다.
func CheckCAFile(path string, now time.Time) Check {
	c := Check{Name: "인증서 " + filepath.Base(path)}
	b, err := os.ReadFile(path)
	if err != nil {
		c.Status, c.Detail = "fail", err.Error()
		return c
	}
	n := 0
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			c.Status, c.Detail = "fail", "인증서를 읽지 못함: "+err.Error()
			return c
		}
		n++
		left := cert.NotAfter.Sub(now)
		switch {
		case left < 0:
			c.Status, c.Detail = "fail", fmt.Sprintf("%s 만료됨(%s)", cert.Subject.CommonName, cert.NotAfter.Format("2006-01-02"))
			return c
		case left < 30*24*time.Hour:
			c.Status, c.Detail = "warn", fmt.Sprintf("%s 만료 %d일 전", cert.Subject.CommonName, int(left.Hours()/24))
			return c
		}
	}
	if n == 0 {
		c.Status, c.Detail = "fail", "PEM 인증서가 없음"
		return c
	}
	c.Status, c.Detail = "ok", fmt.Sprintf("인증서 %d개, 모두 30일 넘게 유효", n)
	return c
}

// Doctor 는 설치 전후에 호스트·구성 문제를 점검한다. imageVersion 이 비면 이미지가 필요한 항목은 건너뛴다.
func (in *Installer) Doctor(ctx context.Context) []Check {
	var out []Check
	add := func(c Check) { out = append(out, c) }

	if _, err := in.docker(ctx, nil, "info", "--format", "{{.ServerVersion}}"); err != nil {
		add(Check{"컨테이너 런타임", "fail", "docker 데몬에 접속할 수 없음"})
	} else {
		add(Check{"컨테이너 런타임", "ok", "docker 데몬 응답"})
	}

	_ = os.MkdirAll(in.Dir, 0o755)
	if gb, err := FreeGB(in.Dir); err != nil {
		add(Check{"디스크", "fail", err.Error()})
	} else if gb < float64(in.Cfg.MinDiskGB) {
		add(Check{"디스크", "fail", fmt.Sprintf("남은 %.1fGB < 필요 %dGB", gb, in.Cfg.MinDiskGB)})
	} else {
		add(Check{"디스크", "ok", fmt.Sprintf("남은 %.1fGB (필요 %dGB)", gb, in.Cfg.MinDiskGB)})
	}

	st := LoadState(in.Dir)
	for _, p := range in.Cfg.Ports {
		parts := strings.Split(p, ":")
		if len(parts) < 2 {
			continue
		}
		host, port := "127.0.0.1", parts[0]
		if len(parts) == 3 {
			host, port = parts[0], parts[1]
		}
		if st.Version != "" { // 이미 우리 스택이 쓰고 있을 수 있다
			add(Check{"포트 " + port, "ok", "설치된 스택이 사용 중일 수 있어 점검 생략"})
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort(host, port))
		if err != nil {
			add(Check{"포트 " + port, "fail", "이미 사용 중"})
			continue
		}
		l.Close()
		add(Check{"포트 " + port, "ok", "비어 있음"})
	}

	for _, f := range in.Cfg.CAFiles {
		add(CheckCAFile(f, time.Now()))
	}

	if st.Version != "" {
		if _, err := in.docker(ctx, nil, "exec", in.dbName(), "pg_isready", "-U", "harborline", "-d", "harborline"); err != nil {
			add(Check{"DB 접속", "fail", "DB 컨테이너가 응답하지 않음"})
		} else {
			add(Check{"DB 접속", "ok", "pg_isready 통과"})
			add(in.clockSkew(ctx))
		}
		for _, t := range in.Cfg.TLSChecks {
			args := []string{"run", "--rm", "--network", in.Cfg.Network.Name}
			for _, m := range in.Cfg.Mounts {
				args = append(args, "-v", m.Host+":"+m.Container+":ro")
			}
			args = append(args, "harborline-app:"+st.Version, "tlscheck", t.Addr)
			if t.StartTLS != "" {
				args = append(args, "--starttls", t.StartTLS)
			}
			if t.CAFile != "" {
				args = append(args, "--ca", t.CAFile)
			}
			o, err := in.docker(ctx, nil, args...)
			if err != nil {
				add(Check{"TLS " + t.Addr, "fail", shorten(err.Error())})
			} else {
				add(Check{"TLS " + t.Addr, "ok", strings.TrimSpace(o)})
			}
		}
	} else if len(in.Cfg.TLSChecks) > 0 {
		add(Check{"TLS 종단 점검", "warn", "이미지가 아직 없어 설치 후 `harborctl doctor` 에서 확인"})
	}
	return out
}

func shorten(s string) string {
	if len(s) > 240 {
		return s[:240] + "…"
	}
	return s
}

// clockSkew 는 호스트 시각과 DB 서버 시각의 차이를 본다(인증서 유효성·토큰 만료에 영향).
func (in *Installer) clockSkew(ctx context.Context) Check {
	o, err := in.docker(ctx, nil, "exec", in.dbName(), "psql", "-U", "harborline", "-d", "harborline", "-tA", "-c", "SELECT extract(epoch from now())::bigint")
	if err != nil {
		return Check{"시각", "warn", "DB 시각을 읽지 못함"}
	}
	var sec int64
	fmt.Sscanf(strings.TrimSpace(o), "%d", &sec)
	d := time.Since(time.Unix(sec, 0))
	if d < 0 {
		d = -d
	}
	if d > 5*time.Second {
		return Check{"시각", "fail", fmt.Sprintf("호스트와 DB 서버 시각이 %.0f초 차이", d.Seconds())}
	}
	return Check{"시각", "ok", fmt.Sprintf("호스트와 DB 서버 시각 차이 %.1f초", d.Seconds())}
}

// Failed 는 fail 이 하나라도 있는지 본다.
func Failed(cs []Check) bool {
	for _, c := range cs {
		if c.Status == "fail" {
			return true
		}
	}
	return false
}
