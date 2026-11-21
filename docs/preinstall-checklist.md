# 사전 설치 점검표

고객사 인프라·보안 담당자가 설치 전에 확인하는 목록입니다. `harborctl doctor` 가 자동으로 확인하는 항목에는 표시를 했습니다. 이 저장소는 compose 기준이며 Kubernetes/Helm 은 다루지 않습니다.

## 1. 서버

| 확인 | 기준 | 자동 점검 |
|---|---|---|
| 컨테이너 런타임 | Docker Engine 과 `docker compose` 플러그인이 동작한다. 외부 레지스트리 접속은 필요 없다(이미지는 번들 안 tar 로 들어온다) | doctor: 컨테이너 런타임 |
| 디스크 | 설치 폴더가 있는 파일시스템의 여유 공간이 `install.json` 의 `min_disk_gb`(기본 2GB) 이상이다. 필요한 양은 [사이징 가이드](sizing.md)의 방법으로 직접 측정해서 정한다 | doctor: 디스크 |
| 시각 | 서버 시각이 NTP 로 맞춰져 있다. 인증서 유효 기간과 로그인 토큰 만료 판단에 쓰인다 | doctor: 시각(호스트와 DB 서버 시각 차이 5초 초과 시 실패) |
| 사용자 | 앱 컨테이너는 uid 10001 로 돈다. 첨부 폴더(`<설치 폴더>/data/attachments`)에 이 사용자가 쓸 수 있어야 한다(harborctl 이 만들 때 권한을 맞춘다) | 설치 시 생성 |

## 2. 네트워크

| 확인 | 기준 | 자동 점검 |
|---|---|---|
| 외부 인터넷 | **필요 없다.** 설치·업그레이드·롤백·백업·복구가 모두 외부 접속 없이 동작한다(격리 네트워크 e2e 로 확인) | — |
| 앱 접근 경로 | 기본은 호스트 포트를 열지 않는다. 사내 리버스 프록시·로드밸런서가 앱 컨테이너 `8080` 으로 들어오게 하거나, `install.json` 의 `ports` 로 호스트 포트를 연다 | doctor: 포트(설치 전 `ports` 가 비어 있는지) |
| 사내 서비스 | 아래 3~5 절의 연동 대상(SMTP·LDAP/OIDC·내부 모델 서버)에 앱 컨테이너가 닿는다. 이름 풀이가 필요하면 `install.json` 의 `dns` 를 지정한다 | doctor: TLS 종단 점검(설정한 항목만) |
| 프록시 | 외부로 나가는 길이 사내 HTTP 프록시뿐이면 [방화벽·프록시 요구표](network-requirements.md)의 환경 변수를 설정한다 | — |

## 3. 인증서

| 확인 | 기준 | 자동 점검 |
|---|---|---|
| 사설 CA | 사내 SMTP·LDAP·OIDC·프록시 서버가 사설 CA 인증서를 쓰면 그 CA(PEM)를 `install.json` 의 `mounts` 로 앱에 넣고 `HARBORLINE_CA_FILE` 로 알려 준다. 시스템 CA 에 더해 이 CA 도 믿는다 | doctor: 인증서 파일(만료·읽기 오류는 실패, 30일 미만은 경고) |
| 체인 | 서버가 중간 인증서까지 보내야 한다. `install.json` 의 `tls_checks` 에 종단을 적어 두면 doctor 가 앱 네트워크 안에서 실제 핸드셰이크로 확인한다 | doctor: TLS \<주소\> |
| 검증 끄기 | 인증서 검증을 끄는 옵션은 없다. 검증이 실패하면 호출이 실패한다 | — |

## 4. 계정·권한

| 확인 | 기준 |
|---|---|
| 관리자 | 자체 로그인을 쓰면 `HARBORLINE_ADMIN_PASSWORD` 로 admin 계정이 만들어진다. 설치 후 바꾸고 `install.json` 에서 지운다 |
| SSO | OIDC 는 발급자 URL·클라이언트 ID·시크릿, 그룹 클레임 이름, `그룹=역할` 매핑이 필요하다. LDAP 은 URL, 검색용 계정, 사용자·그룹 검색 기준과 `그룹=역할` 매핑이 필요하다. 매핑된 그룹이 없는 사용자는 로그인돼도 403 이다 |
| 메일 | SMTP 호스트·포트, STARTTLS 사용 여부, 인증 정보, 발신 주소. STARTTLS 를 요구했는데 서버가 지원하지 않으면 평문으로 보내지 않고 실패한다 |

## 5. 번들 수령

| 확인 | 기준 |
|---|---|
| 서명 공개키 | 릴리스 공개키(`release.pub`)를 번들과 **다른 경로**로 받아 보관한다. harborctl 은 이 키로 서명된 번들만 설치한다 |
| 번들 | `manifest.json`, `manifest.sig`, `images/`, `migrations/` 가 모두 있다. 파일 하나라도 바뀌면 설치가 거부된다 |

## 설치 순서

```bash
harborctl doctor  --dir /opt/harborline --config install.json   # 위 자동 점검 항목
harborctl install --dir /opt/harborline --config install.json \
                  --bundle ./harborline-v1.0.0 --pubkey ./release.pub
harborctl status  --dir /opt/harborline
```
