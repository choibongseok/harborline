// go test -json 결과에서 실패한 테스트의 이름과 출력 전문을 보여 준다. 사용: node scripts/show-failures.mjs .test-results/e2e.json
import { readFileSync } from 'node:fs';

const events = readFileSync(process.argv[2], 'utf8').split('\n').filter((l) => l.startsWith('{')).map((l) => JSON.parse(l));
const out = new Map();
const failed = new Set();
for (const e of events) {
  if (!e.Test) continue;
  if (e.Action === 'output') out.set(e.Test, (out.get(e.Test) ?? '') + e.Output);
  if (e.Action === 'fail') failed.add(e.Test);
}
if (failed.size === 0) {
  const pkgFail = events.filter((e) => e.Action === 'fail' && !e.Test).map((e) => e.Package);
  console.error(`실패한 개별 테스트는 없음(패키지 실패: ${pkgFail.join(', ') || '없음'}). 빌드 오류나 시간 초과일 수 있음:`);
  for (const e of events.filter((x) => x.Action === 'output' && !x.Test)) process.stderr.write(e.Output);
  process.exit(0);
}
for (const t of failed) {
  console.error(`\n=== 실패: ${t} ===`);
  process.stderr.write(out.get(t) ?? '(출력 없음)');
}
