// llmcompat 는 모의 내부 모델 서버의 모든 프로필에 같은 시나리오를 돌려 호환성 표를 출력한다.
//
//	llmcompat [--json out.json] [--md out.md]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/choibongseok/harborline/internal/llm/compat"
)

func main() {
	jsonOut := flag.String("json", "", "결과 JSON 파일")
	mdOut := flag.String("md", "", "결과 마크다운 파일")
	flag.Parse()
	rows := compat.Run(context.Background())
	md := compat.Markdown(rows)
	fmt.Print(md)
	if *jsonOut != "" {
		b, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile(*jsonOut, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *mdOut != "" {
		if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	for _, r := range rows {
		for _, c := range []compat.Cell{r.Classify, r.DraftShort, r.DraftLong} {
			if c.Status == compat.Failed {
				fmt.Fprintf(os.Stderr, "실패: %s %+v\n", r.Profile, c)
				os.Exit(1)
			}
		}
	}
}
