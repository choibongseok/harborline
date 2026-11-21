// docs/TEST_CASES.md 와 실제 테스트 결과(go test -json, Playwright json)를 대조한다.
// 행 수 = 실행된 테스트 수, 모든 행이 실제로 통과한 테스트 하나와 1:1, 종류 열이 파일 위치와 일치, 요약 표의 개수가 맞는지 본다.
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

const MODULE = 'github.com/choibongseok/harborline/';
const results = '.test-results';
const doc = readFileSync('docs/TEST_CASES.md', 'utf8');
const problems = [];
const actual = new Map(); // "파일::테스트" -> status

function testFuncs(dir) {
  const m = new Map();
  for (const f of readdirSync(dir).filter((x) => x.endsWith('_test.go'))) {
    for (const x of readFileSync(join(dir, f), 'utf8').matchAll(/^func (Test[A-Za-z0-9_]+)\(/gm)) m.set(x[1], join(dir, f));
  }
  return m;
}

for (const name of ['unit', 'integration', 'e2e']) {
  const p = join(results, `${name}.json`);
  if (!existsSync(p)) {
    problems.push(`${p} 가 없음: make verify 로 테스트를 먼저 실행해야 함`);
    continue;
  }
  const funcCache = new Map();
  for (const line of readFileSync(p, 'utf8').split('\n')) {
    if (!line.trim().startsWith('{')) continue;
    const ev = JSON.parse(line);
    if (!ev.Test || ev.Test.includes('/') || !['pass', 'fail', 'skip'].includes(ev.Action)) continue;
    const dir = ev.Package.startsWith(MODULE) ? ev.Package.slice(MODULE.length) : '.';
    if (!funcCache.has(dir)) funcCache.set(dir, testFuncs(dir));
    const file = funcCache.get(dir).get(ev.Test);
    if (!file) {
      problems.push(`${ev.Package}.${ev.Test}: 소스에서 찾지 못함`);
      continue;
    }
    actual.set(`${file}::${ev.Test}`, ev.Action === 'pass' ? 'passed' : ev.Action);
  }
}

// 브라우저 테스트(Playwright)
const pw = join(results, 'web.json');
if (existsSync(pw)) {
  const walk = (s) => {
    for (const sp of s.specs ?? []) actual.set(`web/e2e/${sp.file}::${sp.title}`, sp.ok ? 'passed' : 'failed');
    for (const sub of s.suites ?? []) walk(sub);
  };
  for (const s of JSON.parse(readFileSync(pw, 'utf8')).suites ?? []) walk(s);
} else {
  problems.push(`${pw} 가 없음: 브라우저 테스트 결과가 필요함`);
}

const rows = doc
  .split('\n')
  .filter((l) => /^\| [A-Z0-9]+-\d{3} \|/.test(l))
  .map((l) => l.split(' | ').map((c) => c.replace(/^\| /, '').replace(/ \|$/, '').trim()));

const kindOf = (f) => (f.startsWith('test/integration/') ? 'integration' : f.startsWith('test/e2e/') || f.startsWith('web/e2e/') ? 'e2e' : 'unit');
const perKind = { unit: 0, integration: 0, e2e: 0 };
const ids = new Set();
const mapped = new Set();
for (const cells of rows) {
  if (cells.length !== 7) {
    problems.push(`열 수가 7 이 아님: ${cells[0]}`);
    continue;
  }
  const [id, , , , , ref, kind] = cells;
  if (ids.has(id)) problems.push(`ID 중복: ${id}`);
  ids.add(id);
  const key = ref.replace(/^`|`$/g, '');
  if (!actual.has(key)) problems.push(`${id}: 실제 테스트 없음 -> ${key}`);
  else if (actual.get(key) !== 'passed') problems.push(`${id}: 통과하지 않음(${actual.get(key)}) -> ${key}`);
  if (mapped.has(key)) problems.push(`${id}: 같은 테스트가 두 행에 매핑됨 -> ${key}`);
  mapped.add(key);
  const k = kindOf(key.split('::')[0]);
  if (k !== kind) problems.push(`${id}: 종류가 ${kind} 인데 파일은 ${k}`);
  perKind[k]++;
}
for (const key of actual.keys()) if (!mapped.has(key)) problems.push(`문서에 없는 테스트: ${key}`);
if (rows.length !== actual.size) problems.push(`문서 행 ${rows.length} != 실행된 테스트 ${actual.size}`);

const total = Number(/\*\*합계\*\* \| \*\*(\d+)\*\*/.exec(doc)?.[1]);
if (total !== actual.size) problems.push(`요약 표의 합계 ${total} != 실행된 테스트 ${actual.size}`);
for (const k of ['unit', 'integration', 'e2e']) {
  const m = new RegExp(`\\| ${k} [^|]*\\| (\\d+) \\|`).exec(doc);
  if (!m || Number(m[1]) !== perKind[k]) problems.push(`요약 표의 ${k} 개수 ${m?.[1]} != ${perKind[k]}`);
}

if (problems.length) {
  console.error(`테스트 케이스 문서 대조 실패 (${problems.length}건)`);
  for (const p of problems.slice(0, 40)) console.error(' - ' + p);
  process.exit(1);
}
console.log(`테스트 케이스 문서 대조 통과: 문서 ${rows.length}행 = 실행 ${actual.size}건 (unit ${perKind.unit}, integration ${perKind.integration}, e2e ${perKind.e2e})`);
