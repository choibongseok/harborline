# 포트·방화벽 요구표

기본 구성은 호스트에 포트를 열지 않고, 앱·DB 컨테이너가 하나의 도커 네트워크 안에서만 통신합니다. 아래는 이 구성이 실제로 쓰는 연결입니다.

## 인바운드

| 대상 | 포트 | 누가 접속 | 비고 |
|---|---|---|---|
| 앱 컨테이너 | 8080/tcp | 사내 리버스 프록시·로드밸런서 | 기본은 호스트에 열지 않는다. 열려면 `install.json` 의 `"ports": ["127.0.0.1:8080:8080"]` 처럼 지정한다. TLS 종료는 앞단 프록시가 맡는 구성을 가정한다(앱은 평문 HTTP) |
| DB 컨테이너 | 5432/tcp | 앱 컨테이너(같은 네트워크) | 호스트에 열지 않는다. 백업·복구는 `docker exec` 로 컨테이너 안에서 실행한다 |

## 아웃바운드(앱 컨테이너 → 고객 환경)

| 목적 | 기본 포트 | 프로토콜 | 설정 | 프록시 경유 |
|---|---|---|---|---|
| 메일 | 587/tcp (설정) | SMTP + STARTTLS | `HARBORLINE_SMTP_HOST`, `HARBORLINE_SMTP_PORT`, `HARBORLINE_SMTP_STARTTLS` | 아니오(SMTP 는 HTTP 프록시를 쓰지 않는다) |
| 내부 모델 서버 | 설정 | HTTP(S), OpenAI 호환(`/v1/models`, `/v1/chat/completions`) | `HARBORLINE_LLM_BASE_URL` | `NO_PROXY` 에 넣으면 직접, 아니면 프록시 |
| LDAP | 389/tcp 또는 636/tcp | LDAP / LDAPS (STARTTLS 선택) | `HARBORLINE_LDAP_URL`, `HARBORLINE_LDAP_STARTTLS` | 아니오 |
| OIDC 발급자 | 443/tcp | HTTPS(디스커버리, 토큰, 키 목록) | `HARBORLINE_OIDC_ISSUER` | 프록시 설정을 따른다 |
| 이름 풀이 | 53/udp,tcp | DNS | `install.json` 의 `dns` | — |

**설치형 프로필(`HARBORLINE_PROFILE=onprem`)은 이 표 밖으로 나가는 호출 경로가 없습니다.** 클라우드 어댑터(S3, 클라우드 메일 API, 호스팅 LLM)는 설치형 프로필에서 설정 검증 단계에서 거부되고, 통합 테스트가 전체 시나리오 동안 나가는 HTTP 목적지가 설정한 내부 모델 서버 하나뿐임을 확인합니다.

## 사내 HTTP 프록시만 열려 있는 경우

| 환경 변수 | 뜻 |
|---|---|
| `HARBORLINE_HTTPS_PROXY` | 외부로 나가는 HTTP(S) 호출이 거치는 프록시. 예: `http://proxy.corp.example:3128` |
| `HARBORLINE_NO_PROXY` | 프록시를 거치지 않을 호스트·도메인·CIDR(쉼표 구분). 사내 모델 서버처럼 직접 가야 하는 곳을 넣는다 |
| `HARBORLINE_CA_FILE` | 프록시가 TLS 를 사설 CA 로 다시 열면(검사형 프록시) 그 CA 인증서(PEM) |

프로세스 전역 환경 변수(`HTTPS_PROXY`)가 아니라 이 설정만 읽습니다. 사설 CA 를 주지 않으면 프록시가 연 TLS 는 인증서 검증에서 막히고 호출이 실패합니다(e2e 로 확인).

## 방화벽 요청 예시(담당자에게 보낼 양식)

```
출발: harborline 서버(앱 컨테이너가 속한 도커 네트워크의 호스트)
목적: SMTP 서버  tcp/587 | 내부 모델 서버 tcp/<포트> | LDAP 서버 tcp/389(or 636) | OIDC 발급자 tcp/443
목적: 사내 DNS udp,tcp/53
외부 인터넷: 요청하지 않음
```
