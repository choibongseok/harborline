#!/usr/bin/env bash
# 5분 실행: 격리 네트워크(외부로 나가는 길 없음)에 고객 사이트 모의(SMTP·내부 모델 서버·싱크홀·게이트웨이)를 만들고 harborctl 로 설치한다.
#   bash scripts/demo.sh up     # 번들이 없으면 만들고, v1.0.0 설치까지
#   bash scripts/demo.sh upgrade v1.1.0
#   bash scripts/demo.sh down
# 앱은 게이트웨이를 통해서만 열린다: http://127.0.0.1:58181  (관리자 admin / admin-pw)
set -euo pipefail
cd "$(dirname "$0")/.."
export HL_SITE_NAME=hl-demo-site HL_SITE_NET=hl-demo-site HL_EDGE_NET=hl-demo-edge HL_APP=hl-demo-app HL_GATEWAY_PORT=58181
export HL_SUBNET=10.78.0.0/24 HL_RANGE=10.78.0.128/25 HL_SINK_IP=10.78.0.2
export HL_CERTS="$PWD/dist/demo-certs"
INST="$PWD/dist/demo-install"
site() { docker compose -f test/e2e/site.compose.yml "$@"; }
ctl() { dist/bin/harborctl "$@" --dir "$INST"; }

case "${1:-up}" in
  up)
    [ -f dist/bundles/harborline-v1.0.0/manifest.json ] || go run ./cmd/hlbuild bundle
    mkdir -p dist/bin
    go build -o dist/bin/harborctl ./cmd/harborctl
    go run ./cmd/hltools gencerts --cert "$HL_CERTS"
    CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -o dist/build/tools/hltools ./cmd/hltools
    cp deploy/Dockerfile.tools dist/build/tools/Dockerfile
    docker build -q -t harborline-tools:e2e dist/build/tools >/dev/null
    site up -d
    mkdir -p "$INST"
    cat > "$INST/install.json" <<JSON
{
  "project": "hl-demo",
  "network": {"name": "hl-demo-site", "external": true},
  "dns": ["10.78.0.2"],
  "health_timeout_sec": 30,
  "min_disk_gb": 1,
  "ca_files": ["$HL_CERTS/ca.pem"],
  "mounts": [{"host": "$HL_CERTS/ca.pem", "container": "/etc/harborline/ca.pem"}],
  "tls_checks": [{"addr": "smtp.corp.test:587", "starttls": "smtp", "ca_file": "/etc/harborline/ca.pem"}],
  "env": {
    "HARBORLINE_SMTP_HOST": "smtp.corp.test", "HARBORLINE_SMTP_PORT": "587", "HARBORLINE_CA_FILE": "/etc/harborline/ca.pem",
    "HARBORLINE_LLM_BASE_URL": "http://llm.corp.test:8000", "HARBORLINE_ADMIN_PASSWORD": "admin-pw"
  }
}
JSON
    ctl install --bundle dist/bundles/harborline-v1.0.0 --config "$INST/install.json" --pubkey dist/keys/release.pub
    echo
    echo "앱: http://127.0.0.1:58181/api/system   (문의 접수: POST /api/inquiries, 로그인: POST /auth/login)"
    echo "업그레이드: bash scripts/demo.sh upgrade v1.1.0   정리: make down"
    ;;
  upgrade)
    ctl upgrade --bundle "dist/bundles/harborline-${2:?버전(예: v1.1.0)}" --pubkey dist/keys/release.pub
    ;;
  down)
    [ -x dist/bin/harborctl ] && ctl destroy || true
    site down -v --remove-orphans
    ;;
  *) echo "사용: demo.sh up|upgrade <버전>|down" >&2; exit 1 ;;
esac
