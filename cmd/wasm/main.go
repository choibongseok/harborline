//go:build js && wasm

// 브라우저용 진입점. 업그레이드 판정(release.Upgrade)과 마이그레이션 규약 검사(release.Lint)를 그대로 노출한다.
package main

import (
	"syscall/js"

	"github.com/choibongseok/harborline/internal/sim"
)

func wrap(f func(string) (string, error)) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		out, err := f(args[0].String())
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return map[string]any{"json": out}
	})
}

func main() {
	js.Global().Set("harborline", map[string]any{"upgrade": wrap(sim.Upgrade), "lint": wrap(sim.Lint)})
	select {}
}
