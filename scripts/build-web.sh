#!/usr/bin/env bash
# 데모 사이트(정적)를 web/dist 에 만든다. 판정 로직은 cmd/wasm 을 WebAssembly 로 컴파일해 싣는다(규칙을 JS 로 다시 쓰지 않는다).
# 모든 URL 이 상대 경로라 GitHub Pages 프로젝트 경로(/harborline/)에서도, 로컬 어디서든 같게 동작한다.
set -euo pipefail
cd "$(dirname "$0")/.."
for f in docs/evidence/manifests.json docs/evidence/e2e-transcripts.json docs/evidence/llm-compat.json; do
  [ -s "$f" ] || { echo "증거 파일이 없음: $f (make evidence 또는 make verify 를 먼저 실행)" >&2; exit 1; }
done
[ -d web/dist ] && rm -r web/dist
mkdir -p web/dist/data
GOOS=js GOARCH=wasm go build -trimpath -ldflags='-s -w' -o web/dist/harborline.wasm ./cmd/wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/dist/
cp web/src/index.html web/src/style.css web/src/app.js web/dist/
cp docs/evidence/manifests.json web/dist/data/manifests.json
cp docs/evidence/e2e-transcripts.json web/dist/data/e2e.json
cp docs/evidence/llm-compat.json web/dist/data/llm-compat.json
touch web/dist/.nojekyll
if grep -nE '(src|href)="/[^/]' web/dist/index.html; then echo "절대 경로 참조가 있음(Pages 하위 경로에서 깨짐)" >&2; exit 1; fi
echo "web/dist 생성: $(du -sh web/dist | cut -f1) (wasm $(du -h web/dist/harborline.wasm | cut -f1))"
