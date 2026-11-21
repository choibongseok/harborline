# Harborline 진입점. push 전에는 `make verify` 가 0 으로 끝나야 한다.
.PHONY: up down test test-unit test-int e2e bundle compat evidence web pages verify infra infra-down

# 5분 실행: 번들 빌드 → 격리 네트워크 고객 사이트 모의 → harborctl 로 v1.0.0 설치 (앱 http://127.0.0.1:58181)
up:
	bash scripts/demo.sh up

down:
	bash scripts/demo.sh down

# 단위 + 통합(실제 PG·LDAP·Keycloak 필요 → infra 가 올린다). e2e 는 시간이 걸려 따로 돈다.
test: test-unit test-int

test-unit:
	go test -count=1 ./...

infra:
	bash scripts/infra.sh

infra-down:
	docker compose -f test/infra/compose.yml down -v --remove-orphans

test-int: infra
	go test -count=1 -tags integration ./test/integration

# 릴리스 번들(이미지 tar·마이그레이션·매니페스트·ed25519 서명). 마이그레이션 규약 검사를 먼저 통과해야 한다.
bundle:
	go run ./cmd/hlbuild bundle

# 격리 네트워크 e2e(설치·업그레이드·실패 주입·롤백·백업 복구·프록시·사설 CA)
e2e: bundle
	CGO_ENABLED=0 GOOS=linux GOARCH=$$(go env GOARCH) go build -o dist/build/tools/hltools ./cmd/hltools
	cp deploy/Dockerfile.tools dist/build/tools/Dockerfile
	docker build -q -t harborline-tools:e2e dist/build/tools
	go test -count=1 -tags e2e -timeout 40m ./test/e2e

# 모의 내부 모델 서버 프로필별 호환성 표
compat:
	go run ./cmd/llmcompat

evidence:
	node scripts/evidence.mjs

web:
	bash scripts/build-web.sh

verify:
	bash scripts/verify.sh

# 데모 사이트를 gh-pages 브랜치에 로컬 커밋한다(push 는 하지 않는다).
pages:
	bash scripts/deploy-pages.sh
