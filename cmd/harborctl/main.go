// harborctl 은 설치형 릴리스의 설치·업그레이드·롤백·백업·복구·사전 점검을 한다.
//
//	harborctl install  --bundle DIR --dir DIR --config install.json --pubkey release.pub
//	harborctl upgrade  --bundle DIR --dir DIR --pubkey release.pub
//	harborctl rollback --dir DIR
//	harborctl backup   --dir DIR
//	harborctl restore  --dir DIR [--backup ID|latest]
//	harborctl doctor   --dir DIR [--config install.json]
//	harborctl status   --dir DIR
//
// 종료 코드: 0 성공, 2 거부(서명·체크섬·버전 게이트), 3 중단(백업·적재 실패), 4 실패 후 자동 복귀, 5 정지(복구 필요), 1 그 외 오류
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/choibongseok/harborline/internal/ctl"
	"github.com/choibongseok/harborline/internal/release"
)

func fail(code int, err error) {
	fmt.Fprintln(os.Stderr, "harborctl:", err)
	os.Exit(code)
}

func exitFor(o release.Outcome) int {
	switch o {
	case release.OutcomeSuccess:
		return 0
	case release.OutcomeRejected:
		return 2
	case release.OutcomeAborted:
		return 3
	case release.OutcomeRolledBack:
		return 4
	default:
		return 5
	}
}

func readPub(path string) (ed25519.PublicKey, error) {
	if path == "" {
		return nil, errors.New("--pubkey 가 필요함(번들 서명 검증용 공개키)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("공개키 형식 오류")
	}
	return ed25519.PublicKey(raw), nil
}

func main() {
	if len(os.Args) < 2 {
		fail(1, errors.New("사용: harborctl install|upgrade|rollback|backup|restore|doctor|status|destroy"))
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", "./harborline-install", "설치 폴더")
	bundle := fs.String("bundle", "", "번들 폴더")
	conf := fs.String("config", "", "install.json (install 에서 최초 1회)")
	pub := fs.String("pubkey", "", "서명 검증 공개키 파일")
	backup := fs.String("backup", "latest", "복구할 백업 id")
	asJSON := fs.Bool("json", false, "doctor/status 를 JSON 으로 출력")
	_ = fs.Parse(os.Args[2:])
	ctx := context.Background()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fail(1, err)
	}
	if *conf != "" {
		b, err := os.ReadFile(*conf)
		if err != nil {
			fail(1, err)
		}
		if err := os.WriteFile(filepath.Join(*dir, "install.json"), b, 0o600); err != nil {
			fail(1, err)
		}
	}
	cfg, err := ctl.LoadConfig(*dir)
	if err != nil {
		fail(1, fmt.Errorf("설치 설정을 읽지 못함(--config install.json 로 최초 지정): %w", err))
	}
	if err := cfg.Save(*dir); err != nil { // 비밀 값 생성 후 저장
		fail(1, err)
	}
	in := &ctl.Installer{Dir: *dir, Cfg: cfg, Out: os.Stdout}
	if *pub != "" {
		if in.Pub, err = readPub(*pub); err != nil {
			fail(1, err)
		}
	}

	switch cmd {
	case "install", "upgrade":
		if *bundle == "" {
			fail(1, errors.New("--bundle 이 필요함"))
		}
		if in.Pub == nil {
			fail(1, errors.New("--pubkey 가 필요함(서명을 검증하지 않고는 설치하지 않음)"))
		}
		st := ctl.LoadState(*dir)
		if cmd == "install" && st.Version != "" {
			fail(1, fmt.Errorf("이미 %s 가 설치되어 있음: upgrade 를 쓰세요", st.Version))
		}
		if cmd == "upgrade" && st.Version == "" {
			fail(1, errors.New("설치된 버전이 없음: install 을 쓰세요"))
		}
		if cmd == "install" {
			cs := in.Doctor(ctx)
			printChecks(os.Stdout, cs)
			if ctl.Failed(cs) {
				fail(1, errors.New("사전 점검에 실패해 설치하지 않음"))
			}
		}
		res, err := in.Upgrade(ctx, *bundle)
		if err != nil {
			fail(1, err)
		}
		os.Exit(exitFor(res.Outcome))
	case "rollback":
		if err := in.Rollback(ctx); err != nil {
			fail(1, err)
		}
		fmt.Println("롤백 완료")
	case "backup":
		id, err := in.Backup(ctx)
		if err != nil {
			fail(1, err)
		}
		fmt.Println("백업 id:", id)
	case "restore":
		if err := in.Restore(ctx, *backup); err != nil {
			fail(1, err)
		}
	case "doctor":
		cs := in.Doctor(ctx)
		if *asJSON {
			_ = json.NewEncoder(os.Stdout).Encode(cs)
		} else {
			printChecks(os.Stdout, cs)
		}
		if ctl.Failed(cs) {
			os.Exit(1)
		}
	case "status":
		st := ctl.LoadState(*dir)
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(b))
		if st.Status == "running" {
			if err := in.WaitHealthy(ctx); err != nil {
				fail(1, err)
			}
			fmt.Println("상태 점검: 통과")
		}
	case "destroy":
		_ = in.Destroy(ctx)
	default:
		fail(1, fmt.Errorf("알 수 없는 명령: %s", cmd))
	}
	_ = asJSON
}

func printChecks(w io.Writer, cs []ctl.Check) {
	for _, c := range cs {
		fmt.Fprintf(w, "[%-4s] %-28s %s\n", c.Status, c.Name, c.Detail)
	}
}
