// harborline 은 문의함 서버다. 하위 명령: serve(기본), migrate, healthcheck, tlscheck.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/choibongseok/harborline/internal/config"
	"github.com/choibongseok/harborline/internal/httpx"
	"github.com/choibongseok/harborline/internal/migrate"
	"github.com/choibongseok/harborline/internal/wiring"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "migrate":
		err = runMigrate(os.Args[2:])
	case "healthcheck":
		err = healthcheck()
	case "tlscheck":
		err = tlscheck(os.Args[2:])
	default:
		err = fmt.Errorf("알 수 없는 명령: %s", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "harborline:", err)
		os.Exit(1)
	}
}

func serve() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv, closeFn, err := wiring.Build(ctx, cfg, os.Getenv("HARBORLINE_FEATURES"), os.Getenv("HARBORLINE_BREAK"), wiring.Options{Log: log})
	if err != nil {
		return err
	}
	defer closeFn()
	hs := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = hs.Shutdown(sctx)
	}()
	log.Info("시작", "version", cfg.Version, "profile", cfg.Profile, "listen", cfg.Listen, "features", os.Getenv("HARBORLINE_FEATURES"))
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// runMigrate 는 --dir 의 마이그레이션을 DB 에 적용한다.
func runMigrate(args []string) error {
	dir := "/migrations"
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--dir" {
			dir = args[i+1]
		}
	}
	url := os.Getenv("HARBORLINE_DATABASE_URL")
	if url == "" {
		return errors.New("HARBORLINE_DATABASE_URL 이 필요함")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	files, err := migrate.LoadDir(dir)
	if err != nil {
		return err
	}
	applied, err := migrate.Apply(ctx, pool, files)
	for _, a := range applied {
		fmt.Println("적용:", a)
	}
	return err
}

// healthcheck 는 컨테이너 안에서 자기 /healthz 를 확인한다(이미지에 curl 이 없다).
func healthcheck() error {
	addr := os.Getenv("HARBORLINE_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	_, port, _ := net.SplitHostPort(addr)
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("healthz %d", resp.StatusCode)
	}
	return nil
}

// tlscheck host:port [--starttls smtp] [--ca file] 는 인증서 체인이 신뢰되는지 확인한다.
func tlscheck(args []string) error {
	if len(args) < 1 {
		return errors.New("사용: tlscheck host:port [--starttls smtp] [--ca file]")
	}
	addr, startTLS, ca := args[0], "", ""
	for i := 1; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--starttls":
			startTLS = args[i+1]
		case "--ca":
			ca = args[i+1]
		}
	}
	pool, err := httpx.CertPool(ca)
	if err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	tc := &tls.Config{ServerName: host, RootCAs: pool, MinVersion: tls.VersionTLS12}
	if startTLS == "smtp" {
		if err := smtpStartTLS(conn, host, tc); err != nil {
			return err
		}
		fmt.Println("ok: STARTTLS 인증서 체인 검증 통과")
		return nil
	}
	t := tls.Client(conn, tc)
	if err := t.Handshake(); err != nil {
		return err
	}
	fmt.Println("ok: TLS 인증서 체인 검증 통과, 만료", t.ConnectionState().PeerCertificates[0].NotAfter.Format("2006-01-02"))
	return nil
}
