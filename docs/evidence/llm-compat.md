| 프로필 | 도구 호출 | JSON 모드 | 컨텍스트(토큰) | 분류(구조화 출력) | 답변 초안(짧은 스레드) | 답변 초안(긴 스레드) |
|---|---|---|---|---|---|---|
| full | 지원 | 지원 | 8192 | 통과 (tools) | 통과 (direct) | 통과 (direct) |
| no-tools | 미지원 | 지원 | 8192 | 대체 경로 (json_mode) | 통과 (direct) | 통과 (direct) |
| no-json | 미지원 | 미지원 | 8192 | 대체 경로 (prompt_json) | 통과 (direct) | 통과 (direct) |
| short-ctx | 지원 | 지원 | 1024 | 통과 (tools) | 통과 (direct) | 대체 경로 (chunked) |
| bare | 미지원 | 미지원 | 1024 | 대체 경로 (prompt_json) | 통과 (direct) | 대체 경로 (chunked) |
| unreachable | 미지원 | 미지원 | - | 비활성 | 비활성 | 비활성 |
