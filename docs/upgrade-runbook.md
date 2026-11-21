# 업그레이드·롤백 런북

대상: 고객사 운영 담당자. 모든 명령은 `harborctl` 이 설치 폴더(`--dir`)의 `install.json`·`state.json` 을 읽어 실행합니다. 번들 서명 검증용 공개키는 `--pubkey` 로 줍니다.

## 0. 시작 전에

1. `harborctl status --dir <폴더>` 가 `상태 점검: 통과` 이고 `state.json` 의 `status` 가 `running` 인지 본다. `halted` 이면 먼저 아래 5절로 복구한다(정지 상태에서는 업그레이드를 거부한다).
2. 새 번들의 `manifest.json` 에서 `version`, `min_from`, `schema` 를 읽는다.
   - 현재 버전이 `min_from` 보다 낮으면 업그레이드할 수 없다(건너뛴 버전). 중간 릴리스 번들을 먼저 올린다.
   - `schema` 값에 따라 실패 시 동작이 다르다(아래 표).
3. 점검: `harborctl doctor --dir <폴더>`.

| `schema` | 뜻 | 단계가 실패하면 |
|---|---|---|
| `none` | 스키마 변경 없음 | 이전 버전으로 **자동 복귀**하고 상태 점검을 다시 통과해야 성공으로 본다 |
| `compatible` | expand/contract 규약을 지킨 변경. 이전 버전 앱이 변경된 스키마에서도 동작한다 | 마이그레이션이 중간에 실패해도 **자동 복귀**한다 |
| `breaking` | 이전 버전 앱이 동작하지 않을 수 있는 변경 | 자동 복귀하지 않고 **정지**한다. 백업 복구(5절) |

## 1. 업그레이드

```bash
harborctl upgrade --dir /opt/harborline --bundle ./harborline-v1.2.0 --pubkey ./release.pub
```

단계(출력에 `[ok]`/`[fail]`/`[skip]` 로 나온다):

| 단계 | 하는 일 | 실패하면 |
|---|---|---|
| verify | 서명 검증, 매니페스트의 모든 파일 SHA-256 대조 | 거부(종료 코드 2). 아무것도 바뀌지 않음. 번들을 다시 받는다 |
| gate | 현재 버전 ≥ `min_from`, 대상 > 현재 | 거부(종료 코드 2) |
| backup | DB 덤프 + 설정 + 첨부 → `<폴더>/backups/<id>/` | 중단(종료 코드 3). 운영 중인 버전 그대로 |
| load | 번들의 이미지 tar 를 `docker load` | 중단(종료 코드 3) |
| migrate | 번들의 마이그레이션을 파일마다 한 트랜잭션으로 적용(스키마 변경이 없으면 건너뜀) | 위 표대로 |
| switch | 새 버전 이미지로 앱 컨테이너 교체 | 위 표대로 |
| health | 앱 컨테이너 안에서 `/healthz` 가 통과할 때까지 `health_timeout_sec` 동안 확인 | 위 표대로 |

종료 코드: `0` 성공, `2` 거부, `3` 중단, `4` 실패 후 자동 복귀, `5` 정지(복구 필요), `1` 그 외 오류.

## 2. 자동 복귀가 일어났을 때(종료 코드 4)

- 출력의 `rollback` 줄이 `이전 버전으로 복귀, 상태 점검 통과` 인지 확인한다. 이 줄이 있어야 서비스가 이전 버전으로 정상 동작하는 것이다.
- `harborctl status` 로 한 번 더 확인하고, 실패한 단계·사유를 번들 제공자에게 전달한다.
- 스키마가 일부 확장된 채 남아 있을 수 있다(`compatible` 릴리스의 마이그레이션이 중간에 실패한 경우). 이전 버전은 그 상태에서 동작하도록 설계되어 있고, 같은 릴리스를 고쳐서 다시 올리면 남은 마이그레이션부터 이어서 적용된다.

## 3. 수동 롤백

업그레이드가 성공한 뒤 문제가 발견되면(앱만 직전 버전으로):

```bash
harborctl rollback --dir /opt/harborline
```

- 현재 릴리스의 `schema` 가 `none` 또는 `compatible` 일 때만 허용된다. `breaking` 이면 거부하고 5절을 안내한다.
- 새 버전에서 쓴 데이터는 그대로 남는다(데이터를 되돌리는 것이 아니라 앱 버전만 되돌린다).
- 한 단계(직전 버전)까지만 되돌릴 수 있다.

## 4. 백업

```bash
harborctl backup --dir /opt/harborline
```

`<폴더>/backups/<id>/` 에 `db.dump`(pg_dump custom 형식), `attachments.tgz`, `install.json`, `state.json`, `meta.json`(버전·스키마 종류·체크섬)이 생긴다. 업그레이드는 시작 전에 자동으로 한 번 만든다. 백업을 다른 저장소로 옮기는 일정과 보관 기간은 고객사 정책에 따른다.

## 5. 복구(정지 상태이거나 데이터를 되돌려야 할 때)

```bash
harborctl restore --dir /opt/harborline --backup latest      # 또는 백업 id
```

순서: 백업 파일 체크섬 검증(맞지 않으면 복구하지 않음) → 앱 정지 → DB 를 지우고 덤프로 복원 → 첨부 폴더 복원 → 백업 시점의 버전 이미지로 앱 기동 → 상태 점검 통과 확인. 백업 시점의 버전 이미지가 이 서버에 남아 있어야 한다(harborctl 은 이미지를 지우지 않는다).

복구 뒤에는 백업 이후에 쌓인 데이터가 없어진다. 복구 전에 현재 상태를 `harborctl backup` 으로 한 번 더 남기는 것을 권장한다(정지 상태에서도 DB 컨테이너가 떠 있으면 가능).

## 6. 확인 목록

- [ ] `harborctl status` → `상태 점검: 통과`
- [ ] `state.json` 의 `version` 이 의도한 버전
- [ ] 문의 건수·첨부 다운로드 샘플 확인
- [ ] `harborctl doctor` 에 `[fail]` 이 없음
