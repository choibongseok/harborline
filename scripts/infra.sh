#!/usr/bin/env bash
# 통합 테스트용 PostgreSQL·OpenLDAP·Keycloak 을 올리고 준비될 때까지 기다린다(호스트 포트 56433·53890·58480).
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose -f test/infra/compose.yml up -d --wait pg ldap >/dev/null
docker compose -f test/infra/compose.yml up -d keycloak >/dev/null
echo -n "Keycloak 준비 대기"
for i in $(seq 1 90); do
  if curl -fsS -o /dev/null http://localhost:58480/realms/harborline/.well-known/openid-configuration 2>/dev/null; then echo " 완료"; exit 0; fi
  echo -n "."; sleep 2
done
echo; echo "Keycloak 이 준비되지 않음" >&2; exit 1
