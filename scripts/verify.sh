#!/usr/bin/env bash
# 로컬 검증: 형식·정적 검사, 마이그레이션 규약, 단위·통합·e2e, 호환성 표, 증거 수집, 데모 사이트 빌드·브라우저 시험, 테스트 케이스 문서 대조.
# 올린 컨테이너는 끝나면(실패해도) 내린다. 사용하는 호스트 포트: 56433·53890·58480(통합), 58180(e2e 게이트웨이).
set -euo pipefail
cd "$(dirname "$0")/.."
step() { printf '\n== %s ==\n' "$*"; }
cleanup() {
  docker compose -f test/infra/compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
  HL_CERTS="$PWD/dist/e2e-certs" docker compose -f test/e2e/site.compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
  HL_PROXY_CA=/tmp docker compose -f test/e2e/proxy.compose.yml down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
mkdir -p .test-results

step "형식·정적 검사"
test -z "$(gofmt -l .)" || { gofmt -l .; echo "gofmt 필요" >&2; exit 1; }
go vet ./...
GOOS=js GOARCH=wasm go vet ./cmd/wasm
go vet -tags integration ./test/integration
go vet -tags e2e ./test/e2e

step "마이그레이션 규약 검사(빌드 단계 검사)"
go run ./cmd/hlbuild lint

passed() { grep -c '"Action":"pass","Package":[^,]*,"Test":"Test[A-Za-z0-9_]*"' "$1" || true; }

step "단위 테스트"
go test -json -count=1 ./... > .test-results/unit.json || { grep -E '"Action":"fail"' .test-results/unit.json | head; exit 1; }
echo "통과한 단위 테스트: $(passed .test-results/unit.json)"

step "통합 테스트(실제 PostgreSQL·OpenLDAP·Keycloak, STARTTLS SMTP, 모의 내부 모델 서버)"
bash scripts/infra.sh
go test -json -count=1 -tags integration ./test/integration > .test-results/integration.json || { grep -E '"Action":"fail"|_test.go' .test-results/integration.json | head -20; exit 1; }
echo "통과한 통합 테스트: $(passed .test-results/integration.json)"

step "릴리스 번들 빌드(이미지 tar·마이그레이션·매니페스트·서명)"
go run ./cmd/hlbuild bundle
CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -o dist/build/tools/hltools ./cmd/hltools
cp deploy/Dockerfile.tools dist/build/tools/Dockerfile
docker build -q -t harborline-tools:e2e dist/build/tools >/dev/null

step "e2e(격리 네트워크에서 설치 → 업그레이드 → 실패 주입 → 롤백 → 백업·복구, 프록시·사설 CA)"
go test -json -count=1 -tags e2e -timeout 40m ./test/e2e > .test-results/e2e.json || { grep -E '"Action":"fail"|_test.go' .test-results/e2e.json | head -20; exit 1; }
echo "통과한 e2e 시나리오: $(passed .test-results/e2e.json)"

step "LLM 호환성 표"
mkdir -p docs/evidence
go run ./cmd/llmcompat --json docs/evidence/llm-compat.json --md docs/evidence/llm-compat.md

step "증거 수집(매니페스트·e2e 터미널 기록)"
node scripts/evidence.mjs

step "데모 사이트 빌드"
bash scripts/build-web.sh

step "데모 사이트 브라우저 시험(Playwright, 로컬 Chrome)"
(cd web && npm ci --no-audit --no-fund >/dev/null && npx playwright test 2>&1 | grep -E '✓|✘|passed|failed|Error' || true)
test -f .test-results/web.json
node -e 'const r=require("./.test-results/web.json"); console.log(`브라우저 테스트 ${r.stats.expected} 통과, 실패 ${r.stats.unexpected}`); process.exit(r.stats.unexpected?1:0)'

step "테스트 케이스 문서 대조"
node scripts/check-test-cases.mjs

printf '\nverify 통과\n'
