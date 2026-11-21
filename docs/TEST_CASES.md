# 테스트 케이스

모든 행은 실제 테스트 하나와 1:1 로 대응합니다. 자동화 테스트 열은 `파일::테스트 이름`(Go 의 `Test…` 함수, 브라우저 테스트는 Playwright `test` 제목)입니다. 행 수와 실제 테스트 수는 `make verify` 가 `scripts/check-test-cases.mjs` 로 대조합니다(통과하지 않은 테스트, 문서에 없는 테스트, 두 행에 걸친 테스트가 있으면 실패).

통합·e2e 테스트는 실제 PostgreSQL·OpenLDAP·Keycloak 과 격리된 도커 네트워크를 씁니다. `make verify` 가 올리고 끝나면 내립니다.

| 종류 | 개수 | 실행 |
|---|---|---|
| unit (판정 로직·어댑터·설정·번들 검사·호환성 표) | 86 | `make test-unit` |
| integration (실제 PG·LDAP·Keycloak, STARTTLS SMTP) | 19 | `make test-int` |
| e2e (격리 네트워크 설치·업그레이드·복구·프록시 / 데모 사이트 브라우저) | 46 | `make e2e`, `make verify` |
| **합계** | **151** | `make verify` |

| ID | 영역 | 시나리오 | 입력/조건 | 기대 결과 | 자동화 테스트 | 종류 |
|---|---|---|---|---|---|---|
| REL-001 | 릴리스 판정 | 신규 설치는 게이트를 통과한다 | 현재 버전 없음, 대상 v1.0.0(min_from v0.0.0) | allowed, code=ok | `internal/release/gate_test.go::TestGateAllowsFreshInstall` | unit |
| REL-002 | 릴리스 판정 | min_from 과 같은 버전에서는 올릴 수 있다 | 현재 v1.1.0, 대상 v1.2.0(min_from v1.1.0) | allowed | `internal/release/gate_test.go::TestGateAllowsUpgradeAtMinFrom` | unit |
| REL-003 | 릴리스 판정 | 건너뛴 버전은 거부한다 | 현재 v1.0.0, 대상 v1.4.0(min_from v1.3.0) | code=below-min-from | `internal/release/gate_test.go::TestGateRejectsSkippedVersion` | unit |
| REL-004 | 릴리스 판정 | 같은 버전·낮은 버전은 업그레이드가 아니다 | 현재 v1.2.0 에 v1.2.0, v1.1.0 | same-version, downgrade | `internal/release/gate_test.go::TestGateRejectsSameAndDowngrade` | unit |
| REL-005 | 릴리스 판정 | 버전은 숫자로 비교한다 | v1.9.0 → v1.10.0 | allowed(문자열 비교였다면 거부됨) | `internal/release/gate_test.go::TestGateComparesNumerically` | unit |
| REL-006 | 릴리스 판정 | 형식이 틀린 버전은 거부한다 | 현재 "1.x", 대상 "latest" | code=bad-version | `internal/release/gate_test.go::TestGateRejectsMalformedVersion` | unit |
| REL-007 | 릴리스 판정 | 스키마 변경 종류별 롤백 가능 여부 | none, compatible, breaking | 앞의 둘은 가능, breaking 은 불가 | `internal/release/gate_test.go::TestCanRollbackBySchemaKind` | unit |
| MIG-001 | 마이그레이션 규약 | 추가만 하는 expand 는 통과한다 | CREATE TABLE, DEFAULT 가 있는 NOT NULL 컬럼 추가, 인덱스 | 위반 0건 | `internal/release/lint_test.go::TestLintAcceptsAdditiveExpand` | unit |
| MIG-002 | 마이그레이션 규약 | expand 안의 호환 파괴 문장을 모두 잡는다 | DROP COLUMN/TABLE, RENAME, 타입 변경, SET NOT NULL, DEFAULT 없는 NOT NULL 추가, TRUNCATE | 각각 expand-<규칙> 위반 | `internal/release/lint_test.go::TestLintRejectsDestructiveStatementsInExpand` | unit |
| MIG-003 | 마이그레이션 규약 | 주석 속 낱말은 무시한다 | 주석에 DROP COLUMN 이 있는 expand | 위반 0건 | `internal/release/lint_test.go::TestLintIgnoresDestructiveWordsInComments` | unit |
| MIG-004 | 마이그레이션 규약 | contract 는 앞선 expand 를 가리키고 이미 안 쓰는 것만 지운다 | contract-of 누락, 같은 릴리스, unused-since 가 같거나 뒤 | contract-of · contract-release · unused-since 위반, 규약 준수는 통과 | `internal/release/lint_test.go::TestLintContractNeedsEarlierExpandAndUnusedSince` | unit |
| MIG-005 | 마이그레이션 규약 | 파일 번호 순서와 릴리스 순서가 어긋나면 거부한다 | 0001 이 v1.2.0, 0002 가 v1.0.0 | order 위반 | `internal/release/lint_test.go::TestLintRejectsReleaseOrderMismatch` | unit |
| MIG-006 | 마이그레이션 규약 | 릴리스의 스키마 변경 종류를 계산하고 선언과 대조한다 | breaking 마이그레이션이 든 릴리스를 compatible 로 선언 | schema-declared 위반, 정직한 선언은 통과 | `internal/release/lint_test.go::TestClassifyAndCheckDeclared` | unit |
| MIG-007 | 마이그레이션 규약 | release·phase 헤더가 없거나 알 수 없는 phase 는 거부한다 | 헤더 없는 파일, phase: shrink | 파싱 오류 | `internal/release/lint_test.go::TestParseMigrationRequiresHeader` | unit |
| MIG-008 | 마이그레이션 규약 | 버전 파싱과 비교 | v1.2.3 vs 1.10.0, 빈 값·v1·v1.2·v1.2.x·v1..2 | 숫자 순서 비교, 잘못된 형식은 오류 | `internal/release/lint_test.go::TestVersionParseAndCompare` | unit |
| SIG-001 | 서명·체크섬 | 정상 서명은 통과한다 | ed25519 키로 서명한 매니페스트 | 검증 통과 | `internal/release/manifest_test.go::TestSignatureVerifies` | unit |
| SIG-002 | 서명·체크섬 | 변조된 매니페스트는 거부한다 | 서명 뒤 schema 를 none → breaking 으로 수정 | ErrSignature | `internal/release/manifest_test.go::TestSignatureRejectsTamperedManifest` | unit |
| SIG-003 | 서명·체크섬 | 다른 키·깨진 서명 문자열은 거부한다 | 다른 키로 서명, base64 가 아닌 문자열 | ErrSignature | `internal/release/manifest_test.go::TestSignatureRejectsOtherKeyAndGarbage` | unit |
| SIG-004 | 서명·체크섬 | 파일 순서가 달라도 같은 바이트가 나온다 | 같은 파일 목록을 다른 순서로 직렬화 | 바이트 동일(서명 재현) | `internal/release/manifest_test.go::TestManifestMarshalIsDeterministic` | unit |
| SIG-005 | 서명·체크섬 | 파일 내용이 바뀌면 체크섬 불일치를 잡는다 | 매니페스트 해시와 다른 내용 | ErrChecksum | `internal/release/manifest_test.go::TestVerifyFilesDetectsChecksumMismatch` | unit |
| SIG-006 | 서명·체크섬 | 매니페스트에 있는 파일이 없으면 잡는다 | 번들에 파일 없음 | ErrMissing | `internal/release/manifest_test.go::TestVerifyFilesDetectsMissingFile` | unit |
| SIG-007 | 서명·체크섬 | 모두 일치하면 통과한다(목록에 없는 파일은 보지 않는다) | 일치하는 두 파일 + 여분 파일 | 통과 | `internal/release/manifest_test.go::TestVerifyFilesPassesWhenAllMatch` | unit |
| SIG-008 | 서명·체크섬 | 매니페스트 필드를 검증한다 | 알 수 없는 schema, 형식이 틀린 min_from | 오류, 정상은 통과 | `internal/release/manifest_test.go::TestParseManifestValidatesFields` | unit |
| RUN-001 | 업그레이드 상태 기계 | 모든 단계가 통과하면 성공한다 | 스키마 변경 없는 v1.0.0 → v1.1.0 | success, 마이그레이션 호출 없음 | `internal/release/run_test.go::TestUpgradeSucceedsWhenAllStagesPass` | unit |
| RUN-002 | 업그레이드 상태 기계 | 서명 불일치는 검증 단계에서 거부한다 | 서명 불일치 주입 | rejected, 실행기 호출은 verify 한 번뿐 | `internal/release/run_test.go::TestUpgradeRejectsBadSignatureBeforeTouchingAnything` | unit |
| RUN-003 | 업그레이드 상태 기계 | 건너뛴 버전은 백업 전에 거부한다 | v1.0.0 → v1.4.0(min_from v1.3.0) | rejected, backup 호출 없음 | `internal/release/run_test.go::TestUpgradeRejectsSkippedVersionBeforeBackup` | unit |
| RUN-004 | 업그레이드 상태 기계 | 백업이 실패하면 시작하지 않는다 | 백업 실패 주입 | aborted, 이미지 적재 호출 없음 | `internal/release/run_test.go::TestUpgradeAbortsWhenBackupFailsAndKeepsRunningVersion` | unit |
| RUN-005 | 업그레이드 상태 기계 | 이미지 적재가 실패하면 교체하지 않는다 | 적재 실패 주입 | aborted, switch 호출 없음 | `internal/release/run_test.go::TestUpgradeAbortsWhenImageLoadFails` | unit |
| RUN-006 | 업그레이드 상태 기계 | 스키마 변경이 없으면 상태 점검 실패 시 자동 복귀한다 | none 릴리스, 상태 점검 실패 | rolled_back, 복귀 뒤 이전 버전 상태 점검까지 수행 | `internal/release/run_test.go::TestUpgradeAutoRollsBackWhenNoSchemaChangeAndHealthFails` | unit |
| RUN-007 | 업그레이드 상태 기계 | expand/contract 릴리스는 마이그레이션 실패에도 자동 복귀한다 | compatible 릴리스, 마이그레이션 실패 | rolled_back | `internal/release/run_test.go::TestUpgradeAutoRollsBackOnMigrationFailureWhenCompatible` | unit |
| RUN-008 | 업그레이드 상태 기계 | expand/contract 릴리스는 상태 점검 실패에도 자동 복귀한다 | compatible 릴리스, 상태 점검 실패 | rolled_back | `internal/release/run_test.go::TestUpgradeAutoRollsBackOnHealthFailureWhenCompatible` | unit |
| RUN-009 | 업그레이드 상태 기계 | 호환되지 않는 스키마 변경이 실패하면 정지하고 복구를 안내한다 | breaking 릴리스, 상태 점검 실패 | halted, 안내에 restore, 이전 버전 전환 없음 | `internal/release/run_test.go::TestUpgradeHaltsWithoutRollbackWhenBreakingSchemaFails` | unit |
| RUN-010 | 업그레이드 상태 기계 | 복귀 자체가 실패하면 정지한다 | 상태 점검 실패 + 복귀 실패 | halted, 마지막 이벤트가 rollback fail | `internal/release/run_test.go::TestUpgradeHaltsWhenRollbackItselfFails` | unit |
| RUN-011 | 업그레이드 상태 기계 | 신규 설치 실패는 되돌릴 버전이 없어 정지한다 | 현재 버전 없음, 상태 점검 실패 | halted | `internal/release/run_test.go::TestUpgradeFreshInstallFailureHaltsWithoutRollback` | unit |
| RUN-012 | 업그레이드 상태 기계 | 신규 설치는 스키마 변경이 없는 릴리스여도 전체 스키마를 만들고 백업은 건너뛴다 | 현재 버전 없음, 대상 none 릴리스 | migrate 호출, backup 호출 없음 | `internal/release/run_test.go::TestUpgradeFreshInstallAlwaysMigratesAndSkipsBackup` | unit |
| SIM-001 | 브라우저 시뮬레이션 | 시뮬레이션이 release 패키지와 같은 결정을 돌려준다 | 성공, 상태 점검 실패, breaking + 마이그레이션 실패 | success / rolled_back / halted | `internal/sim/sim_test.go::TestSimulationReturnsTheSameDecisionsAsTheReleasePackage` | unit |
| SIM-002 | 브라우저 시뮬레이션 | 게이트·서명·체크섬 거부를 보고한다 | 건너뛴 버전, 서명 불일치, 체크섬 불일치 | rejected, gate 코드 below-min-from | `internal/sim/sim_test.go::TestSimulationReportsGateAndVerificationRejections` | unit |
| SIM-003 | 브라우저 시뮬레이션 | 잘못된 요청은 오류로 돌려준다 | 깨진 JSON, 형식이 틀린 버전 | 오류 | `internal/sim/sim_test.go::TestSimulationRejectsMalformedRequests` | unit |
| SIM-004 | 브라우저 시뮬레이션 | 규약 검사 진입점이 위반과 분류를 돌려준다 | expand 안의 DROP COLUMN, 헤더 없는 파일 | expand-drop-column, parseError | `internal/sim/sim_test.go::TestLintEntryPointFindsViolationsAndClassifies` | unit |
| CTL-001 | harborctl 실행부 | compose 가 기존 네트워크에 붙고 기본은 포트를 열지 않는다 | external 네트워크, DNS, 마운트 설정 | external: true, dns·마운트 포함, ports 없음, read_only | `internal/ctl/ctl_test.go::TestComposeJoinsExistingNetworkAndPublishesNoPortsByDefault` | unit |
| CTL-002 | harborctl 실행부 | internal 네트워크 생성과 env 덮어쓰기 | network.internal=true, env 로 PROFILE 지정 | internal: true, 설정 env 가 기본값을 덮음 | `internal/ctl/ctl_test.go::TestComposeCreatesInternalNetworkWhenAskedAndLetsEnvOverrideDefaults` | unit |
| CTL-003 | harborctl 실행부 | CA 파일의 유효·임박·만료를 구분한다 | 90일, 10일 남음, 만료, 없는 파일 | ok / warn / fail / fail | `internal/ctl/ctl_test.go::TestCheckCAFileClassifiesValidWarnAndExpired` | unit |
| CTL-004 | harborctl 실행부 | 첨부 백업 아카이브 왕복과 경로 이탈 거부 | 정상 파일, ../ 항목이 든 아카이브 | 내용 동일 복원, 이탈은 오류 | `internal/ctl/ctl_test.go::TestAttachmentArchiveRoundTripAndRejectsPathEscape` | unit |
| CTL-005 | harborctl 실행부 | 설정·상태 저장과 비밀 값 생성 | 비밀 값이 빈 설정 저장 | 16자 이상 비밀 생성, 기본값 채움, 상태 왕복 | `internal/ctl/ctl_test.go::TestStateAndConfigPersistWithGeneratedSecrets` | unit |
| BLD-001 | 번들 빌드 검사 | 규약을 어긴 마이그레이션은 번들 빌드 전에 걸린다 | expand 안의 DROP COLUMN | 규약 위반 오류 | `cmd/hlbuild/main_test.go::TestBuildStopsWhenAnExpandMigrationBreaksTheOldVersion` | unit |
| BLD-002 | 번들 빌드 검사 | 선언한 schema 가 계산값과 다르면 거부한다 | 변경이 있는데 none 선언 | 오류 | `cmd/hlbuild/main_test.go::TestBuildStopsWhenDeclaredSchemaDiffersFromMigrations` | unit |
| BLD-003 | 번들 빌드 검사 | 규약을 지킨 릴리스는 통과한다 | CREATE TABLE 만 있는 expand | 통과 | `cmd/hlbuild/main_test.go::TestLintPassesForCompliantRelease` | unit |
| CFG-001 | 설정·프로필 | 설치형 프로필 기본값은 고객 쪽 어댑터다 | PROFILE=onprem | disk·smtp·internal, 검증 통과 | `internal/config/config_test.go::TestProfileOnpremDefaultsToCustomerSideAdapters` | unit |
| CFG-002 | 설정·프로필 | 클라우드 프로필 기본값은 호스팅 어댑터다 | PROFILE=cloud | s3·api·hosted | `internal/config/config_test.go::TestProfileCloudDefaultsToHostedAdapters` | unit |
| CFG-003 | 설정·프로필 | 설치형 프로필은 클라우드 어댑터를 거부한다 | onprem 에 s3·메일 API·호스팅 LLM 지정 | 세 가지 모두 오류에 포함 | `internal/config/config_test.go::TestOnpremRejectsCloudAdapters` | unit |
| CFG-004 | 설정·프로필 | 어댑터 필수 설정이 비면 거부한다 | ldap 인증에 url 없음, 알 수 없는 프로필 | 오류 | `internal/config/config_test.go::TestValidateRequiresAdapterSettings` | unit |
| CFG-005 | 설정·프로필 | 그룹=역할 매핑 문자열 파싱 | " support = agent, admins=admin ,broken, =x" | 올바른 두 쌍만 | `internal/config/config_test.go::TestParseRoleMap` | unit |
| NET-001 | 프록시·사설 CA | 프록시와 사설 CA 로 외부 호출이 된다 | 검사형 프록시, CA 파일 지정 | 응답 수신, 프록시에 CONNECT 기록 | `internal/httpx/client_test.go::TestProxyWithPrivateCAReachesExternalHost` | unit |
| NET-002 | 프록시·사설 CA | 사설 CA 를 모르면 인증서 검증에서 실패한다 | 프록시 있음, CA 파일 없음 | certificate 오류 | `internal/httpx/client_test.go::TestProxyWithoutPrivateCAFailsCertificateVerification` | unit |
| NET-003 | 프록시·사설 CA | NO_PROXY 대상은 프록시를 거치지 않는다 | NO_PROXY=llm.corp.test | 직접 연결, 프록시 기록 0건 | `internal/httpx/client_test.go::TestNoProxyHostsBypassTheProxy` | unit |
| NET-004 | 프록시·사설 CA | 잘못된 CA 파일은 거부한다 | PEM 아님, 없는 파일 | 오류 | `internal/httpx/client_test.go::TestNewRejectsBadCAFile` | unit |
| STO-001 | 저장소 어댑터 | 로컬 디스크 저장·조회 | 한글 내용 저장 | 같은 내용 반환 | `internal/adapters/storage/storage_test.go::TestDiskPutGetRoundTrip` | unit |
| STO-002 | 저장소 어댑터 | 경로 이탈 키를 거부한다 | ../x, a/../../x, 빈 키 | 오류 | `internal/adapters/storage/storage_test.go::TestDiskRejectsPathTraversal` | unit |
| STO-003 | 저장소 어댑터 | S3 SigV4 서명이 공개된 예시와 같다 | AWS 문서의 GET Object 예시 입력 | 서명 f0e8bdb8… | `internal/adapters/storage/storage_test.go::TestS3SignatureMatchesPublishedExample` | unit |
| STO-004 | 저장소 어댑터 | S3 호환 저장소 저장·조회 | 모의 S3 서버, 한글 키 | 저장·조회 성공, 없는 객체는 오류 | `internal/adapters/storage/storage_test.go::TestS3PutGetAgainstFakeServer` | unit |
| MAIL-001 | 메일 어댑터 | 사설 CA 서버로 STARTTLS 를 열어 메일을 보낸다 | 서버 인증서는 사설 CA 발급 | TLS 로 한 통 전달 | `internal/adapters/mail/mail_test.go::TestSMTPStartTLSWithPrivateCADeliversMail` | unit |
| MAIL-002 | 메일 어댑터 | 믿지 않는 CA 의 인증서는 거부한다 | 다른 CA 만 신뢰 | STARTTLS 오류, 메일 미전달 | `internal/adapters/mail/mail_test.go::TestSMTPRejectsServerCertFromUntrustedCA` | unit |
| MAIL-003 | 메일 어댑터 | STARTTLS 미지원 서버에 평문으로 보내지 않는다 | StartTLS=true, 서버는 미지원 | 오류, 메일 미전달 | `internal/adapters/mail/mail_test.go::TestSMTPRefusesPlaintextWhenStartTLSRequiredButUnsupported` | unit |
| MAIL-004 | 메일 어댑터 | 제목의 개행으로 헤더를 주입할 수 없다 | 제목에 \r\nBcc: 포함 | Bcc 헤더 없음 | `internal/adapters/mail/mail_test.go::TestSMTPStripsHeaderInjection` | unit |
| MAIL-005 | 메일 어댑터 | 메일 API 는 JSON POST 와 토큰으로 보낸다 | 토큰 지정, 5xx 응답 | Authorization 전송, 5xx 는 오류 | `internal/adapters/mail/mail_test.go::TestAPIMailerPostsJSONWithToken` | unit |
| AUTH-001 | 인증 어댑터 | 세션 쿠키 왕복과 변조 거부 | 서명 일부 변경, 다른 키 | 정상은 복원, 변조는 거부 | `internal/adapters/auth/auth_test.go::TestSessionRoundTripAndTamper` | unit |
| AUTH-002 | 인증 어댑터 | 세션은 만료된다 | TTL 1분 후 2분 경과 | 거부 | `internal/adapters/auth/auth_test.go::TestSessionExpires` | unit |
| AUTH-003 | 인증 어댑터 | 그룹을 역할로 매핑한다 | dev·support·admins | agent·admin, 매핑 없으면 ErrNoRole | `internal/adapters/auth/auth_test.go::TestMapRolesFromGroups` | unit |
| AUTH-004 | 인증 어댑터 | admin 은 agent 권한을 포함한다 | 역할 검사 | admin→agent 가능, agent→admin 불가 | `internal/adapters/auth/auth_test.go::TestAdminImpliesAgent` | unit |
| AUTH-005 | 인증 어댑터 | 자체 로그인은 bcrypt 해시를 확인한다 | 맞는/틀린 비밀번호, 없는 사용자 | 맞으면 통과, 나머지는 인증 실패 | `internal/adapters/auth/auth_test.go::TestLocalLoginChecksBcryptHash` | unit |
| AUTH-006 | 인증 어댑터 | 세션 쿠키로 인증한다 | 쿠키 있음/없음 | 있으면 사용자, 없으면 인증 실패 | `internal/adapters/auth/auth_test.go::TestLocalAuthenticateReadsSessionCookie` | unit |
| LLM-001 | LLM 어댑터 | 모든 기능을 지원하는 서버를 탐지한다 | full 프로필 | 도구 호출·JSON 모드·컨텍스트 8192 | `internal/llm/llm_test.go::TestProbeFullProfileDetectsAllFeatures` | unit |
| LLM-002 | LLM 어댑터 | 도구 호출 미지원을 탐지한다 | no-tools 프로필 | Tools=false, JSONMode=true | `internal/llm/llm_test.go::TestProbeDetectsMissingToolCalling` | unit |
| LLM-003 | LLM 어댑터 | JSON 모드를 무시하는 서버를 탐지한다 | no-json 프로필(요청은 성공, 응답은 문장) | JSONMode=false | `internal/llm/llm_test.go::TestProbeDetectsIgnoredJSONMode` | unit |
| LLM-004 | LLM 어댑터 | 짧은 컨텍스트를 측정한다 | short-ctx 프로필(1024) | ContextTokens=1024 | `internal/llm/llm_test.go::TestProbeMeasuresShortContext` | unit |
| LLM-005 | LLM 어댑터 | 닿지 않는 엔드포인트는 실패 대신 기능을 끈다 | 접속 불가 주소 | Reachable=false, 사유 기록 | `internal/llm/llm_test.go::TestProbeUnreachableEndpointDisablesInsteadOfFailing` | unit |
| LLM-006 | LLM 어댑터 | 코드 펜스·앞뒤 설명이 붙은 JSON 도 읽는다 | 순수 JSON, ```json 펜스, 설명 + JSON, JSON 없음 | 앞의 셋은 파싱, 마지막은 오류 | `internal/llm/llm_test.go::TestParseLooseJSONHandlesFencesAndProse` | unit |
| LLM-007 | LLM 어댑터 | 도구 호출을 지원하면 도구 호출로 분류한다 | full 프로필 | path=tools, account/긴급 | `internal/llm/llm_test.go::TestClassifyUsesToolsWhenSupported` | unit |
| LLM-008 | LLM 어댑터 | 도구 호출이 없으면 JSON 모드로 대체한다 | no-tools 프로필 | path=json_mode | `internal/llm/llm_test.go::TestClassifyFallsBackToJSONMode` | unit |
| LLM-009 | LLM 어댑터 | 둘 다 없으면 프롬프트 JSON 과 관대한 파싱으로 대체한다 | no-json 프로필(펜스 붙은 응답) | path=prompt_json | `internal/llm/llm_test.go::TestClassifyFallsBackToPromptJSONWithFencedReply` | unit |
| LLM-010 | LLM 어댑터 | 엔드포인트가 죽어 있으면 분류 기능을 끈다 | 접속 불가 | ErrDisabled | `internal/llm/llm_test.go::TestClassifyDisabledWhenEndpointDown` | unit |
| LLM-011 | LLM 어댑터 | 스레드가 컨텍스트에 들어가면 한 번에 초안을 만든다 | full 프로필, 긴 스레드 | path=direct | `internal/llm/llm_test.go::TestDraftDirectWhenThreadFitsContext` | unit |
| LLM-012 | LLM 어댑터 | 짧은 컨텍스트에서는 앞부분을 요약해 초안을 만든다 | short-ctx 프로필, 긴 스레드 | path=chunked, 마지막 메시지 기반 초안 | `internal/llm/llm_test.go::TestDraftChunksLongThreadOnShortContext` | unit |
| LLM-013 | LLM 어댑터 | 컨텍스트가 너무 작으면 초안 기능을 끈다 | ContextTokens=64 | ErrDisabled | `internal/llm/llm_test.go::TestDraftDisabledWhenContextTooSmallForAnything` | unit |
| CMP-001 | LLM 호환성 표 | 프로필마다 통과/대체/비활성 판정이 기대와 같다 | 모의 서버 5종 + 접속 불가 | 6행 모두 기대 표와 일치 | `internal/llm/compat/compat_test.go::TestCompatibilityMatrixMatchesExpectedStatusesForEveryProfile` | unit |
| CMP-002 | LLM 호환성 표 | 대체 경로 이름이 표에 남는다 | 프로필별 분류·긴 스레드 경로 | tools/direct, json_mode/direct, prompt_json/direct, tools/chunked, prompt_json/chunked | `internal/llm/compat/compat_test.go::TestCompatibilityPathsNameTheFallbackUsed` | unit |
| APP-001 | 서비스(실제 PostgreSQL) | 문의 접수 → 답변 → STARTTLS 메일 → 상태 변경 | 실제 PG, 사설 CA SMTP, 설치형 프로필 | 로그인 전 401, 메일 TLS 1통, answered, 통계 | `test/integration/app_test.go::TestInquiryFlowOnRealPostgresWithOnpremAdapters` | integration |
| APP-002 | 서비스(실제 PostgreSQL) | 첨부 업로드·다운로드(로컬 디스크) | 10KB 첨부, ../ 가 든 이름 | 체크섬·내용 동일, 경로 문자는 422 | `test/integration/app_test.go::TestAttachmentRoundTripOnLocalDisk` | integration |
| APP-003 | 서비스(실제 PostgreSQL) | 답변 초안이 내부 모델 서버로 만들어진다 | full 프로필 모의 서버 | 분류 account, path=tools, 초안 포함 | `test/integration/app_test.go::TestDraftSuggestionUsesInternalModelEndpoint` | integration |
| APP-004 | 서비스(실제 PostgreSQL) | 모델 서버가 죽어도 나머지 기능은 정상이다 | 접속 불가 LLM 주소 | 초안만 503, 조회는 200 | `test/integration/app_test.go::TestDraftIsUnavailableButServiceKeepsWorkingWhenModelEndpointIsDown` | integration |
| APP-005 | 서비스(실제 PostgreSQL) | 상태 점검이 버전·스키마를 보고하고 깨진 빌드는 503 | 정상/의도적으로 깨진 빌드 | 200 + 버전·0002, 503 | `test/integration/app_test.go::TestHealthzReportsVersionAndSchemaAndFailsWhenBuildIsBroken` | integration |
| SCH-001 | 스키마·마이그레이션(실제 PostgreSQL) | 마이그레이션은 한 번만 적용된다 | 같은 파일을 두 번 적용 | 첫 번째 2개, 두 번째 0개 | `test/integration/schema_test.go::TestMigrationsApplyOnceAndAreIdempotent` | integration |
| SCH-002 | 스키마·마이그레이션(실제 PostgreSQL) | 중간 실패 시 앞 파일은 남고 실패한 파일은 통째로 롤백된다 | 정상 파일 뒤에 실패하는 파일 | 적용 2개, 실패 파일의 컬럼 없음 | `test/integration/schema_test.go::TestMigrationFailureMidwayKeepsEarlierFilesAndRollsBackTheFailedOne` | integration |
| SCH-003 | 스키마·마이그레이션(실제 PostgreSQL) | 적용된 뒤 바뀐 마이그레이션은 거부한다 | 적용된 파일 내용 수정 | "바뀌었음" 오류 | `test/integration/schema_test.go::TestAppliedMigrationThatChangedIsRejected` | integration |
| SCH-004 | 스키마·마이그레이션(실제 PostgreSQL) | expand 된 스키마에서도 이전 버전 앱이 동작한다 | v1.1 앱이 확장된 스키마에 쓰기 | 정상 동작, 새 버전이 읽으면 state 동기화 | `test/integration/schema_test.go::TestPreviousAppVersionStillWorksOnExpandedSchema` | integration |
| SCH-005 | 스키마·마이그레이션(실제 PostgreSQL) | 마이그레이션 없이 새 버전을 띄우면 쓰기가 실패한다 | v1.2 앱 + v1.0 스키마 | 500 | `test/integration/schema_test.go::TestNewAppVersionFailsWithoutItsMigration` | integration |
| SCH-006 | 스키마·마이그레이션(실제 PostgreSQL) | 호환되지 않는 스키마는 이전 버전을 깨뜨린다 | status 컬럼 삭제 후 v1.2 앱·v2 앱 | 이전 버전 쓰기 500, 새 버전 정상 | `test/integration/schema_test.go::TestBreakingSchemaBreaksThePreviousVersionButNotTheNewOne` | integration |
| ONP-001 | 폐쇄망 코드 경로 | 설치형 프로필은 설정한 내부 모델 서버 외 HTTP 호출이 없다 | 나가는 요청 기록기, 전체 시나리오 | 목적지 1곳(내부 모델), 메일은 고객 SMTP | `test/integration/onprem_test.go::TestOnpremProfileMakesNoOutboundHTTPOtherThanTheConfiguredInternalModel` | integration |
| ONP-002 | 폐쇄망 코드 경로 | 같은 코어에 클라우드 프로필을 주면 호스팅 어댑터로 나간다 | 기록기, S3·메일 API·LLM 모의 | 목적지 3곳 | `test/integration/onprem_test.go::TestCloudProfileUsesTheHostedAdapters` | integration |
| ONP-003 | 폐쇄망 코드 경로 | 설치형 프로필에서 S3 를 고르면 서버가 뜨지 않는다 | STORAGE=s3 | Build 오류 | `test/integration/onprem_test.go::TestOnpremProfileRefusesToBuildWithCloudAdapters` | integration |
| DIR-001 | SSO·디렉터리 연동(실제 서버) | LDAP 로그인과 그룹 → 역할 매핑 | 실제 OpenLDAP, support 그룹 사용자 | 로그인 200, agent 권한 조회 | `test/integration/auth_test.go::TestLDAPLoginMapsGroupsToRoles` | integration |
| DIR-002 | SSO·디렉터리 연동(실제 서버) | LDAP 오류·권한·주입 방어 | 틀린 비밀번호, 그룹 없는 사용자, *, 필터 주입, 빈 비밀번호 | 401 / 403 / 401 / 401 / 401 | `test/integration/auth_test.go::TestLDAPRejectsWrongPasswordUnmappedGroupAndInjection` | integration |
| DIR-003 | SSO·디렉터리 연동(실제 서버) | OIDC ID 토큰의 그룹 클레임을 역할로 매핑한다 | 실제 Keycloak, support·admins 사용자 토큰 | 조회 200 | `test/integration/auth_test.go::TestOIDCBearerTokenMapsGroupClaimToRole` | integration |
| DIR-004 | SSO·디렉터리 연동(실제 서버) | OIDC 오류 처리 | 그룹 없는 사용자, 서명 깨진 토큰, 토큰 없음 | 403 / 401 / 401 | `test/integration/auth_test.go::TestOIDCRejectsUnmappedGroupBadTokenAndMissingToken` | integration |
| DIR-005 | SSO·디렉터리 연동(실제 서버) | OIDC 인가 코드 흐름이 세션을 발급한다 | Keycloak 로그인 폼 제출까지 | 콜백이 roles=agent 반환, 세션으로 조회 200 | `test/integration/auth_test.go::TestOIDCAuthorizationCodeFlowIssuesSession` | integration |
| E2E-001 | 설치·업그레이드·복구(격리 네트워크) | 대조군: 싱크홀이 외부 이름 조회를 기록한다 | 같은 네트워크에서 control.invalid 조회 | 싱크홀 로그에 기록(0건 판정에 의미를 준다) | `test/e2e/scenarios_test.go::TestSinkholeRecordsExternalLookupsAsPositiveControl` | e2e |
| E2E-002 | 설치·업그레이드·복구(격리 네트워크) | 인터넷 없는 네트워크에 신규 설치하고 설치형 어댑터로 동작한다 | v1.0.0 설치, 문의·첨부·답변·초안 | disk·smtp·internal, TLS 메일 1통, 싱크홀 0건 | `test/e2e/scenarios_test.go::TestFreshInstallRunsOfflineWithCustomerSideAdapters` | e2e |
| E2E-003 | 설치·업그레이드·복구(격리 네트워크) | 스키마 변경 없는 업그레이드는 데이터를 보존한다 | v1.0.0 → v1.1.0 | 성공, 백업 생성, 건수·체크섬 동일, /api/stats 생김 | `test/e2e/scenarios_test.go::TestUpgradeWithoutSchemaChangeKeepsData` | e2e |
| E2E-004 | 설치·업그레이드·복구(격리 네트워크) | expand 마이그레이션 업그레이드는 컬럼을 더하고 데이터를 보존한다 | v1.0.0 → v1.2.0 | 성공, state 백필, 건수·체크섬 동일 | `test/e2e/scenarios_test.go::TestUpgradeWithExpandMigrationAddsColumnsAndKeepsData` | e2e |
| E2E-005 | 설치·업그레이드·복구(격리 네트워크) | 상태 점검 실패 시 이전 버전으로 자동 복귀한다 | v1.1.0 → v1.1.1(상태 점검 실패) | 종료 코드 4, 이전 버전 상태 점검 통과, 데이터 동일 | `test/e2e/scenarios_test.go::TestFailedHealthCheckWithoutSchemaChangeRollsBackAutomatically` | e2e |
| E2E-006 | 설치·업그레이드·복구(격리 네트워크) | 마이그레이션 중간 실패에도 이전 버전이 서비스한다 | v1.1.0 → v1.2.1(두 번째 마이그레이션 실패) | 종료 코드 4, 0003 까지 적용, 이전 버전 쓰기 정상 | `test/e2e/scenarios_test.go::TestMigrationFailingMidwayKeepsPreviousVersionServing` | e2e |
| E2E-007 | 설치·업그레이드·복구(격리 네트워크) | 호환되지 않는 스키마 실패는 정지하고 백업 복구로 되살린다 | v1.2.0 → v2.0.0(상태 점검 실패) | 종료 코드 5, 앱 정지, 재시도 거부, 복구 뒤 건수·체크섬 동일 | `test/e2e/scenarios_test.go::TestBreakingSchemaFailureHaltsAndBackupRestoreRecovers` | e2e |
| E2E-008 | 설치·업그레이드·복구(격리 네트워크) | 서명이 맞지 않는 번들은 아무것도 바꾸기 전에 거부한다 | v1.2.2(신뢰하지 않는 키로 서명) | 종료 코드 2, 상태·백업 변화 없음 | `test/e2e/scenarios_test.go::TestBundleWithWrongSignatureIsRejectedBeforeAnyChange` | e2e |
| E2E-009 | 설치·업그레이드·복구(격리 네트워크) | 이미지가 변조된 번들은 체크섬으로 거부한다 | v1.2.3(이미지 tar 에 1바이트 추가) | 종료 코드 2 | `test/e2e/scenarios_test.go::TestBundleWithTamperedImageIsRejectedByChecksum` | e2e |
| E2E-010 | 설치·업그레이드·복구(격리 네트워크) | 건너뛴 버전은 매니페스트 게이트가 거부한다 | v1.0.0 → v1.4.0(min_from v1.3.0) | 종료 코드 2, 변화 없음 | `test/e2e/scenarios_test.go::TestSkippedVersionIsRejectedByManifestGate` | e2e |
| E2E-011 | 설치·업그레이드·복구(격리 네트워크) | upgrade 로 낮은 버전을 올리려 하면 거부한다 | v1.2.0 에 v1.1.0 번들 | 종료 코드 2 | `test/e2e/scenarios_test.go::TestDowngradeThroughUpgradeIsRejected` | e2e |
| E2E-012 | 설치·업그레이드·복구(격리 네트워크) | 호환 가능한 업그레이드 뒤 수동 롤백은 새 버전에서 쓴 데이터도 유지한다 | v1.1.0 → v1.2.0, 데이터 추가, rollback | v1.1.0 복귀, 건수·체크섬 동일 | `test/e2e/scenarios_test.go::TestManualRollbackAfterCompatibleUpgradeKeepsData` | e2e |
| E2E-013 | 설치·업그레이드·복구(격리 네트워크) | 호환되지 않는 스키마 상태에서는 수동 롤백을 거부한다 | state 의 schema 를 breaking 으로 둠 | 거부, 복구 안내 | `test/e2e/scenarios_test.go::TestManualRollbackIsRefusedWhenCurrentReleaseChangedSchemaIncompatibly` | e2e |
| E2E-014 | 설치·업그레이드·복구(격리 네트워크) | 백업 → 훼손 → 복구가 건수·체크섬·첨부를 되돌린다 | 문의 삭제 + 첨부 삭제 + 신규 데이터 후 restore | 백업 시점과 건수·체크섬 동일, 첨부 다운로드 가능 | `test/e2e/scenarios_test.go::TestBackupAndRestoreReturnsDataAndAttachmentsToTheSnapshot` | e2e |
| E2E-015 | 설치·업그레이드·복구(격리 네트워크) | 변조된 백업은 복구하지 않는다 | 덤프 파일에 1바이트 추가 | 체크섬 오류, 데이터 변동 없음 | `test/e2e/scenarios_test.go::TestRestoreRefusesTamperedBackup` | e2e |
| E2E-016 | 설치·업그레이드·복구(격리 네트워크) | 정상 설치에서 doctor 가 통과하고 사설 CA 체인을 확인한다 | 설치 후 doctor | 런타임·디스크·인증서·DB·시각·TLS 항목, fail 없음 | `test/e2e/scenarios_test.go::TestDoctorPassesOnHealthyInstallIncludingPrivateCAChain` | e2e |
| E2E-017 | 설치·업그레이드·복구(격리 네트워크) | doctor 가 만료된 인증서와 디스크 부족을 잡는다 | 만료된 CA 파일, 필요 용량 과다 | fail 항목 포함, 종료 코드 1 | `test/e2e/scenarios_test.go::TestDoctorFailsOnExpiredCertificateAndShortDisk` | e2e |
| E2E-018 | 설치·업그레이드·복구(격리 네트워크) | 사전 점검이 실패하면 설치하지 않는다 | 디스크 요구량 과다 | 종료 코드 1, 컨테이너 미생성 | `test/e2e/scenarios_test.go::TestInstallRefusesToStartWhenPreflightFails` | e2e |
| E2E-019 | 설치·업그레이드·복구(격리 네트워크) | 전 시나리오 동안 외부 접속 시도가 0건이다 | 싱크홀 기록(대조군 제외) | 0건 | `test/e2e/scenarios_test.go::TestZeroExternalConnectionAttemptsRecordedAcrossAllScenarios` | e2e |
| PRX-001 | 프록시·사설 CA(격리 네트워크) | 외부 호출만 프록시를 타고 내부 호출은 직접 간다 | HTTPS_PROXY, NO_PROXY=내부 모델·SMTP, 사설 CA 지정 | 프록시에 메일 API CONNECT 만, 내부 호출 0건, 외부 도착 1회, 싱크홀 0건 | `test/e2e/proxy_test.go::TestOnlyExternalCallsGoThroughProxyAndInternalCallsStayDirect` | e2e |
| PRX-002 | 프록시·사설 CA(격리 네트워크) | 프록시의 사설 CA 를 모르면 호출이 막힌다 | CA 미지정 | mailed=false, 프록시엔 도달, 외부엔 미도달 | `test/e2e/proxy_test.go::TestProxyTLSInterceptionFailsClosedWithoutThePrivateCA` | e2e |
| WEB-001 | 데모 사이트(브라우저) | WASM 기동과 기본 결과 | 페이지 열기 | 자동 복귀 결과, 8단계, 상시 배너, 콘솔 오류 0 | `web/e2e/demo.spec.ts::판정 엔진(WebAssembly)이 뜨고 기본 시나리오 결과가 나오며 콘솔 오류가 없다` | e2e |
| WEB-002 | 데모 사이트(브라우저) | 정상 시나리오 | 정상적으로 올라감 | 종료 코드 0, migrate SKIP, health OK | `web/e2e/demo.spec.ts::정상 업그레이드: 스키마 변경이 없으면 마이그레이션을 건너뛰고 성공한다` | e2e |
| WEB-003 | 데모 사이트(브라우저) | 자동 복귀 시나리오 | 새 버전이 안 뜸 시나리오 | 종료 코드 4, health FAIL, rollback OK | `web/e2e/demo.spec.ts::상태 점검 실패: 스키마 변경이 없으면 이전 버전으로 자동 복귀한다` | e2e |
| WEB-004 | 데모 사이트(브라우저) | 정지 시나리오 | DB 구조가 바뀜 + 실패 시나리오 | 종료 코드 5, harborctl restore 안내, rollback SKIP | `web/e2e/demo.spec.ts::호환되지 않는 스키마 변경이 실패하면 자동 복귀 없이 정지하고 복구를 안내한다` | e2e |
| WEB-005 | 데모 사이트(브라우저) | 건너뛴 버전 시나리오 | v1.0.0 → v1.4.0 | gate FAIL, backup 미실행 | `web/e2e/demo.spec.ts::건너뛴 버전은 게이트에서 거부되고 백업도 만들지 않는다` | e2e |
| WEB-006 | 데모 사이트(브라우저) | 검증 거부 시나리오 | 서명 불일치, 이미지 변조 | verify 단계 거부, 종료 코드 2 | `web/e2e/demo.spec.ts::서명 불일치와 이미지 변조는 검증 단계에서 거부된다` | e2e |
| WEB-007 | 데모 사이트(브라우저) | 수동 조작 | 버전 선택과 실패 주입 라디오 | 판정 갱신, 시나리오 버튼 선택 해제 | `web/e2e/demo.spec.ts::직접 고른 버전과 실패 주입이 판정에 반영되고 시나리오 선택 표시가 풀린다` | e2e |
| WEB-008 | 데모 사이트(브라우저) | 실제 실행 기록 표시 | 시나리오 선택 | 해당 harborctl 명령·출력·종료 코드 | `web/e2e/demo.spec.ts::실제 e2e 터미널 출력 탭이 해당 시나리오의 harborctl 실행 기록을 보여 준다` | e2e |
| WEB-009 | 데모 사이트(브라우저) | 호환성 표 표시 | LLM 탭 | 6행, 대체 경로 이름 표시 | `web/e2e/demo.spec.ts::LLM 호환성 표가 실제 실행 결과 파일의 모든 프로필을 보여 준다` | e2e |
| WEB-010 | 데모 사이트(브라우저) | 브라우저 안 규약 검사 | 위반 SQL → 고친 SQL → 헤더 없음 | 위반 규칙 표시 → 통과 → 헤더 안내 | `web/e2e/demo.spec.ts::마이그레이션 규약 검사기가 위반을 잡고 고치면 통과한다` | e2e |
| WEB-011 | 데모 사이트(브라우저) | 키보드·테마 | 화살표 키, 테마 버튼 | 탭 이동, 포커스 윤곽 존재, 테마 전환 | `web/e2e/demo.spec.ts::키보드: 탭을 화살표로 옮기고 포커스 윤곽이 보이며 테마 전환이 동작한다` | e2e |
| WEB-012 | 데모 사이트(브라우저) | 모바일 레이아웃 | 390px 폭 | 한 열, 가로 스크롤 없음 | `web/e2e/demo.spec.ts::모바일 폭에서 한 열로 쌓이고 가로 스크롤이 생기지 않는다` | e2e |
| WEB-013 | 데모 사이트(브라우저) | 정적 배포 경로 검사 | /harborline/ 하위 경로 서빙 | 모든 요청 같은 출처·200 | `web/e2e/demo.spec.ts::외부 요청이 없고 /harborline/ 아래 모든 파일이 정상 응답한다` | e2e |
| TUR-001 | 데모 사이트 설명 따라가기 | 첫 화면(이미 본 방문자) | hl_tour 쿠키가 있는 상태로 페이지 열기 | 제목 '온프렘용 모의 업그레이드'와 탭 제목, 한 문장 설명, 설명 따라가기·직접 해 보기 버튼, 패널 닫힘, 용어 풀이 | `web/e2e/tour.spec.ts::첫 화면에 한 문장 설명과 두 버튼이 있고, 이미 본 방문자에게는 설명 패널이 닫혀 있다` | e2e |
| TUR-002 | 데모 사이트 설명 따라가기 | 장 넘김과 시나리오 연동 | 7장을 차례로 넘김 | 장마다 시나리오 결과 문장 변경, 강조 테두리, 마치기 시 닫힘, 콘솔 오류 0 | `web/e2e/tour.spec.ts::설명 따라가기: 장을 넘기면 해당 시나리오가 자동 실행되고 볼 곳이 강조된다` | e2e |
| TUR-003 | 데모 사이트 설명 따라가기 | 키보드 조작 | ← → 키 | 2/7, 3/7, 다시 2/7 | `web/e2e/tour.spec.ts::설명 따라가기: 좌우 화살표 키로 장을 넘기고 진행 표시가 바뀐다` | e2e |
| TUR-004 | 데모 사이트 설명 따라가기 | 접기·펼치기 | 4장에서 접고 다시 열기 | 다시 여는 버튼 표시, 4/7 유지 | `web/e2e/tour.spec.ts::설명 따라가기: 접으면 다시 여는 버튼이 보이고 펼치면 같은 장에서 이어진다` | e2e |
| TUR-005 | 데모 사이트 설명 따라가기 | 자동 진행 | 자동 진행 켬/끔(간격 0.9초) | 켜면 장 이동, 끄면 이동 멈춤 | `web/e2e/tour.spec.ts::설명 따라가기: 자동 진행을 켜면 다음 장으로 넘어가고 끄면 멈춘다` | e2e |
| TUR-006 | 데모 사이트 설명 따라가기 | 동작 줄이기 | prefers-reduced-motion: reduce | 자동 진행 버튼 비활성, 키보드로는 이동 | `web/e2e/tour.spec.ts::동작 줄이기 설정에서는 자동 진행을 쓰지 않는다` | e2e |
| TUR-007 | 데모 사이트 설명 따라가기 | 390px 하단 시트 | 투어 열고 장 이동 후 맨 아래로 스크롤 | 강조된 곳이 시트 위에 보임, 푸터가 시트에 가리지 않음, 가로 스크롤 없음 | `web/e2e/tour.spec.ts::모바일 폭에서 설명 시트가 본문 끝과 강조된 곳을 가리지 않는다` | e2e |
| TUR-008 | 데모 사이트 설명 따라가기 | 첫 방문 자동 시작 | 쿠키 없이 페이지 열기 | 1/7 장이 열리고 시나리오·강조 적용, 활성 요소는 BODY, 스크롤 위치 0(첫 화면 보존), 콘솔 오류 0 | `web/e2e/tour.spec.ts::처음 온 방문자에게는 설명 패널이 1장부터 저절로 열리고 포커스를 빼앗지 않는다` | e2e |
| TUR-009 | 데모 사이트 설명 따라가기 | 접은 뒤 재방문 | 자동으로 열린 패널을 접고 새로고침 | 패널 닫힘, 쿠키 hl_tour=seen(path /harborline/, SameSite=Lax), 설명 보기 버튼으로 다시 열림 | `web/e2e/tour.spec.ts::설명을 접은 뒤 새로고침하면 저절로 열리지 않고, 버튼으로 다시 열 수 있다` | e2e |
| TUR-010 | 데모 사이트 설명 따라가기 | 끝까지 본 뒤 재방문 | 7장까지 넘기고 마치기 후 새로고침 | 패널 닫힘 | `web/e2e/tour.spec.ts::설명을 끝까지 본 뒤 새로고침하면 저절로 열리지 않는다` | e2e |
| TUR-011 | 데모 사이트 설명 따라가기 | 쿠키 차단 | document.cookie 접근이 예외를 던지는 환경 | 오류 없이 열리고, 접고 새로고침해도 다시 열림 | `web/e2e/tour.spec.ts::쿠키를 쓸 수 없어도 오류 없이 저절로 열린다` | e2e |
| TUR-012 | 데모 사이트 설명 따라가기 | 390px 첫 방문 | 쿠키 없이 390px 로 열기 | 스크롤 없이 한 문장 설명 아래쪽이 시트 위쪽보다 위에 있음 | `web/e2e/tour.spec.ts::모바일 폭에서 저절로 열린 설명 시트가 첫 화면의 한 문장을 가리지 않는다` | e2e |
