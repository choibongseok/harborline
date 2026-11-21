# 사이징 가이드

**이 문서에는 수치가 없습니다.** 이 저장소는 부하 시험을 하지 않았고, 실측하지 않은 값은 적지 않습니다. 고객사 환경에서 필요한 자원을 정하는 방법과, 측정할 때 쓰는 명령만 적습니다.

## 무엇을 재는가

| 자원 | 재는 방법 | 결정에 쓰는 법 |
|---|---|---|
| 앱 CPU·메모리 | `docker stats --no-stream <project>-app` 을 대표 부하(문의 접수·조회·답변·초안) 중에 반복해서 본다 | 피크 시 메모리의 여유를 두고 컨테이너 한도(`mem_limit`)를 정한다 |
| DB CPU·메모리 | `docker stats --no-stream <project>-db` | 같은 방식 |
| DB 크기 | `docker exec <project>-db psql -U harborline -d harborline -tA -c "select pg_size_pretty(pg_database_size('harborline'))"` 를 데이터가 쌓이는 기간 동안 주기적으로 기록 | 기록한 증가 속도 × 보관 기간 |
| 문의 건당 DB 증가량 | 건수(`select count(*) from inquiries`)와 DB 크기의 변화를 같이 기록해 나눈다 | 건수 예측 → DB 크기 |
| 첨부 | `du -sh <설치 폴더>/data/attachments` 와 첨부 건수·평균 크기(`select avg(size_bytes) from attachments`). 업로드 한도는 건당 10MB | 첨부 보관 기간 × 증가 속도 |
| 백업 | `harborctl backup` 을 `time` 으로 재고 `du -sh <설치 폴더>/backups/<id>` 로 크기를 본다 | 백업 보관 개수 × 크기 + 복구에 걸리는 시간(`harborctl restore` 도 같이 잰다) |
| 업그레이드 중단 시간 | 업그레이드 중 `/healthz` 를 1초 간격으로 호출해 실패 구간의 길이를 센다. harborctl 출력의 단계별 줄로 어느 단계가 긴지 본다 | 점검 창 길이 |
| 이미지 | `docker images harborline-app` 로 보관 중인 버전별 크기. harborctl 은 이전 이미지를 지우지 않는다(롤백·복구용) | 보관할 버전 수 × 크기 |
| 내부 모델 서버 | 답변 초안 요청 1건의 지연을 서버 쪽에서 측정한다. 이 서비스는 시작할 때 컨텍스트 길이를 탐지하고(`GET /api/system` 의 `llm.caps`) 긴 스레드는 요약 후 처리하므로, 요청당 모델 호출 수는 스레드 길이에 따라 늘어난다 | 모델 서버의 동시 처리 용량 |

## 정하는 순서

1. 예상 문의 건수/일, 평균 답변 수, 첨부 비율·크기를 고객과 합의한다.
2. 위 표의 방법으로 작은 데이터(수백 건)를 넣고 건당 증가량을 측정한다.
3. 보관 기간을 곱해 DB·첨부·백업 디스크를 정하고, 여유 비율은 고객사 운영 정책에 따른다.
4. CPU·메모리는 대표 부하(예: k6 같은 도구로 접수·조회 API 호출)를 걸고 `docker stats` 의 피크를 본 뒤 정한다.
5. 정한 `min_disk_gb` 를 `install.json` 에 넣어 `harborctl doctor` 가 설치 전에 확인하게 한다.

## 이 저장소에서 확인하지 않은 것

- 동시 사용자 수·처리량·지연 분포, 장기 운영 시 DB 팽창(vacuum), 대용량 첨부(10MB 초과 거부만 확인).
