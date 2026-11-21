// 판정은 Go(WebAssembly)가 한다. 이 파일은 입력을 모아 넘기고 결과를 그린다. 규칙을 다시 구현하지 않는다.
const STAGES = [
  ['verify', '파일이 진짜인지 확인'],
  ['gate', '올려도 되는 버전인지'],
  ['backup', '되돌릴 사본 만들기'],
  ['load', '설치 파일 올리기'],
  ['migrate', 'DB 구조 바꾸기'],
  ['switch', '새 버전으로 교체'],
  ['health', '새 버전이 잘 뜨는지'],
  ['rollback', '이전 버전으로 복귀'],
];
const OUTCOME = {
  success: ['새 버전으로 올렸고, 잘 동작합니다', '업그레이드 성공', 0],
  rejected: ['시작하지 않았습니다. 아무것도 바뀌지 않았습니다', '거부', 2],
  aborted: ['중단했습니다. 쓰던 버전은 그대로입니다', '중단', 3],
  rolled_back: ['이전 버전으로 자동으로 되돌렸습니다', '자동 복귀', 4],
  halted: ['멈췄습니다. 백업으로 복구해야 합니다', '정지 후 복구', 5],
};
const PRESETS = [
  { id: 'ok-none', label: '정상적으로 올라감', cur: 'v1.0.0', tgt: 'v1.1.0', fail: 'none', real: 'TestUpgradeWithoutSchemaChangeKeepsData' },
  { id: 'ok-expand', label: 'DB 구조를 더하는 릴리스', cur: 'v1.0.0', tgt: 'v1.2.0', fail: 'none', real: 'TestUpgradeWithExpandMigrationAddsColumnsAndKeepsData' },
  { id: 'health', label: '새 버전이 안 뜨면 자동으로 되돌림', cur: 'v1.1.0', tgt: 'v1.1.1', fail: 'none', real: 'TestFailedHealthCheckWithoutSchemaChangeRollsBackAutomatically' },
  { id: 'mid', label: 'DB 변경 중간에 실패하면 자동으로 되돌림', cur: 'v1.1.0', tgt: 'v1.2.1', fail: 'migrate', real: 'TestMigrationFailingMidwayKeepsPreviousVersionServing' },
  { id: 'halt', label: 'DB 구조가 바뀐 릴리스가 실패하면 멈춤', cur: 'v1.2.0', tgt: 'v2.0.0', fail: 'health', real: 'TestBreakingSchemaFailureHaltsAndBackupRestoreRecovers' },
  { id: 'sig', label: '서명이 맞지 않음', cur: 'v1.2.0', tgt: 'v1.2.2', fail: 'badSignature', real: 'TestBundleWithWrongSignatureIsRejectedBeforeAnyChange' },
  { id: 'sum', label: '파일이 변조됨', cur: 'v1.2.0', tgt: 'v1.2.3', fail: 'badChecksum', real: 'TestBundleWithTamperedImageIsRejectedByChecksum' },
  { id: 'skip', label: '건너뛴 버전', cur: 'v1.0.0', tgt: 'v1.4.0', fail: 'none', real: 'TestSkippedVersionIsRejectedByManifestGate' },
];
const SAMPLE_SQL = `-- release: v1.3.0
-- phase: expand
-- status 컬럼을 지우는 변경을 expand 단계에 넣으면 구버전 앱이 깨진다.
ALTER TABLE inquiries DROP COLUMN status;
ALTER TABLE inquiries ADD COLUMN channel text NOT NULL;
`;

const $ = (id) => document.getElementById(id);
const el = (tag, props = {}, ...kids) => {
  const n = Object.assign(document.createElement(tag), props);
  for (const k of kids) n.append(k);
  return n;
};
const data = { manifests: [], e2e: {}, llm: [] };
let wasmReady = false;
let pending = null;

async function loadJSON(p, fallback) {
  try {
    const r = await fetch(p);
    if (!r.ok) throw new Error(r.status);
    return await r.json();
  } catch {
    return fallback;
  }
}

async function bootWasm() {
  const go = new Go();
  const res = await WebAssembly.instantiateStreaming(fetch('harborline.wasm'), go.importObject);
  go.run(res.instance);
  for (let i = 0; i < 50 && !window.harborline; i++) await new Promise((r) => setTimeout(r, 20));
  wasmReady = !!window.harborline;
}

const manifestOf = (v) => data.manifests.find((m) => m.version === v);

function fillSelects() {
  const cur = $('current');
  const tgt = $('target');
  cur.replaceChildren(el('option', { value: '', textContent: '(신규 설치)' }));
  for (const m of data.manifests) cur.append(el('option', { value: m.version, textContent: m.version }));
  for (const m of data.manifests) tgt.append(el('option', { value: m.version, textContent: `${m.version}  · schema ${m.schema} · min ${m.min_from}` }));
}

// 릴리스 정의에 박힌 결함(상태 점검이 실패하는 이미지, 잘못 서명된 번들 등)은 선택한 실패 주입과 별개로 항상 적용한다.
function readInput() {
  const fail = document.querySelector('input[name="fail"]:checked').value;
  const m = manifestOf($('target').value) || {};
  const inject = {
    badSignature: fail === 'badSignature' || m.tamper === 'signature',
    badChecksum: fail === 'badChecksum' || m.tamper === 'checksum',
    rollbackBroken: $('rbBroken').checked,
  };
  if (['backup', 'migrate', 'health'].includes(fail)) inject.failAt = fail;
  else if (m.break === 'health') inject.failAt = 'health';
  return { current: $('current').value, target: $('target').value, inject, fail };
}

function run(activePreset) {
  if (!wasmReady) { pending = activePreset; return; }
  const inp = readInput();
  const m = manifestOf(inp.target);
  if (!m) return;
  const out = window.harborline.upgrade(JSON.stringify({ current: inp.current, manifest: { name: m.name, version: m.version, min_from: m.min_from, schema: m.schema, files: m.files }, inject: inp.inject }));
  if (out.error) { $('decision').textContent = out.error; return; }
  render(JSON.parse(out.json), inp, m);
  renderManifest(m);
  renderTerm(activePreset);
  $('target-note').textContent = m.note ? `릴리스 정의: ${m.note}` : '';
}

function stat(label, value, cls = '', gloss = '') {
  const d = el('div', { className: `stat ${cls}` });
  d.append(el('span', { className: 'k', textContent: label }), ' ', el('span', { className: 'v', textContent: value }));
  if (gloss) d.append(el('span', { className: 'gloss', textContent: gloss }));
  return d;
}
const SCHEMA_GLOSS = { none: 'DB 구조를 바꾸지 않는 릴리스', compatible: '이전 버전도 쓸 수 있게 바꾸는 릴리스', breaking: '이전 버전이 못 쓸 수 있게 바꾸는 릴리스' };

function render(res, inp, m) {
  const [label, sub, code] = OUTCOME[res.outcome];
  const d = $('decision');
  d.dataset.o = res.outcome;
  const main = el('div', { className: 'panel result' });
  main.append(
    el('h3', { className: 'ptitle', textContent: '결과' }),
    el('p', { className: 'what', textContent: label }),
    el('p', { className: 'sublabel', textContent: `결과 종류: ${sub} (${res.outcome})` }),
    el('p', { className: 'meta', textContent: `${inp.current || '신규 설치'} → ${m.version}` }),
  );
  if (res.advice) main.append(el('p', { className: 'advice', textContent: res.advice }));
  main.append(el('p', { className: 'hint', textContent: `근거: ${res.rollbackWhy}` }));
  const stats = el('div', { className: 'stats' });
  stats.append(
    stat('harborctl 종료 코드', String(code), res.outcome, '도구가 끝났을 때 돌려주는 숫자(0 = 성공)'),
    stat('이후 버전', res.currentAfter || '없음', '', '지금 서비스 중인 버전'),
    stat('스키마 변경', res.schema, '', SCHEMA_GLOSS[res.schema]),
    stat('자동 롤백', res.rollbackOk ? '가능' : '불가', res.rollbackOk ? 'okv' : 'badv', res.rollbackOk ? '실패하면 도구가 알아서 이전 버전으로 돌림' : '실패하면 멈추고 백업 복구를 안내'),
  );
  d.replaceChildren(main, stats);

  const byStage = new Map(res.events.map((e) => [e.stage, e]));
  const tl = $('timeline');
  tl.replaceChildren();
  STAGES.forEach(([id, name], i) => {
    const ev = byStage.get(id);
    const li = el('li', { className: ev ? ev.status : 'none' });
    li.style.setProperty('--i', i);
    li.append(
      el('span', { className: `chip ${ev ? ev.status : 'none'}`, textContent: ev ? ev.status.toUpperCase() : '—' }),
      el('span', { className: 'st' }, `${i + 1}. ${name}`, el('small', { textContent: id })),
      el('span', { className: 'detail', textContent: ev ? ev.detail : (id === 'rollback' ? '실패가 없어 실행하지 않음' : '앞 단계에서 끝나 실행하지 않음') }),
    );
    tl.append(li);
  });
}

function renderManifest(m) {
  $('manifest').textContent = JSON.stringify({ name: m.name, version: m.version, min_from: m.min_from, schema: m.schema, files: m.files }, null, 2);
}

function renderTerm(presetId) {
  const box = $('term');
  box.replaceChildren();
  const preset = PRESETS.find((p) => p.id === presetId);
  const recs = preset && data.e2e[preset.real];
  if (!recs) {
    $('term-note').textContent = '이 조합은 실제로 돌려 본 적이 없습니다. 위의 상황 버튼을 누르면 같은 상황을 실제로 실행한 화면이 나옵니다.';
    return;
  }
  $('term-note').textContent = `인터넷이 없는 테스트 환경에서 harborctl 을 실제로 실행한 출력입니다. 테스트 이름: ${preset.real}`;
  for (const r of recs) {
    const pre = el('pre', { className: 'term' });
    pre.append(el('span', { className: 'cmd', textContent: `$ ${r.cmd}\n` }), `${r.output.trimEnd()}\n`, el('span', { className: 'exit', textContent: `(종료 코드 ${r.exit})` }));
    box.append(pre);
  }
}

function renderLLM() {
  const t = $('llm');
  const cls = (s) => (s.startsWith('통과') ? 'pass' : s.startsWith('대체') ? 'fb' : s.startsWith('비활성') ? 'off' : 'bad');
  const cellText = (c) => (c.status === '비활성' || !c.path ? c.status : `${c.status} (${c.path})`);
  t.replaceChildren(el('thead', {}, el('tr', {}, ...['프로필', '도구 호출', 'JSON 모드', '컨텍스트(토큰)', '분류(구조화 출력)', '답변 초안(짧은)', '답변 초안(긴)'].map((h) => el('th', { textContent: h })))));
  const tb = el('tbody');
  for (const r of data.llm) {
    const tr = el('tr');
    tr.append(el('td', { textContent: r.profile }), el('td', { textContent: r.tools ? '지원' : '미지원' }), el('td', { textContent: r.json_mode ? '지원' : '미지원' }), el('td', { textContent: r.reachable ? String(r.context_tokens) : '-' }));
    for (const c of [r.classify, r.draft_short, r.draft_long]) tr.append(el('td', { className: cls(c.status), textContent: cellText(c) }));
    tb.append(tr);
  }
  t.append(tb);
}

function lint() {
  if (!wasmReady) return;
  const out = $('lint-out');
  const out2 = window.harborline.lint(JSON.stringify({ files: [{ name: '0001_change.sql', sql: $('sql').value }], release: $('lint-rel').value.trim(), declared: $('lint-decl').value }));
  out.replaceChildren();
  if (out2.error) { out.textContent = out2.error; return; }
  const r = JSON.parse(out2.json);
  if (r.parseError) { out.append(el('p', { className: 'hint', textContent: r.parseError })); return; }
  if (r.violations.length === 0) { out.append(el('p', { className: 'okmsg', textContent: `규약 통과. 계산된 변경 종류: ${r.classified || 'none'}` })); return; }
  const ul = el('ul', { className: 'viol' });
  for (const v of r.violations) ul.append(el('li', { textContent: `[${v.rule}] ${v.msg}` }));
  out.append(el('p', { className: 'hint', textContent: `설치 파일을 만들 때 거부되는 파일입니다. 계산된 변경 종류: ${r.classified || '-'}` }), ul);
}

function setupTabs() {
  const tabs = [...document.querySelectorAll('[role="tab"]')];
  const select = (t) => {
    for (const x of tabs) {
      const on = x === t;
      x.setAttribute('aria-selected', String(on));
      x.tabIndex = on ? 0 : -1;
      $(x.getAttribute('aria-controls')).hidden = !on;
    }
  };
  tabs.forEach((t, i) => {
    t.addEventListener('click', () => select(t));
    t.addEventListener('keydown', (e) => {
      const k = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
      if (!k) return;
      const n = tabs[(i + k + tabs.length) % tabs.length];
      n.focus();
      select(n);
      e.preventDefault();
    });
  });
}

function setupPresets() {
  const box = $('presets');
  for (const p of PRESETS) {
    const b = el('button', { type: 'button', textContent: p.label });
    b.dataset.id = p.id;
    b.setAttribute('aria-pressed', 'false');
    b.addEventListener('click', () => applyPreset(p));
    box.append(b);
  }
}

let activePreset = null;
function applyPreset(p) {
  activePreset = p.id;
  $('current').value = p.cur;
  $('target').value = p.tgt;
  document.querySelector(`input[name="fail"][value="${p.fail}"]`).checked = true;
  $('rbBroken').checked = false;
  syncPressed();
  run(p.id);
}
function syncPressed() {
  for (const b of $('presets').children) b.setAttribute('aria-pressed', String(b.dataset.id === activePreset));
}
function manual() { activePreset = null; syncPressed(); run(null); }

function setupTheme() {
  const root = document.documentElement;
  let saved = null;
  try { saved = localStorage.getItem('hl-theme'); } catch {}
  const pref = saved || 'dark';
  root.dataset.theme = pref;
  $('theme').addEventListener('click', () => {
    root.dataset.theme = root.dataset.theme === 'dark' ? 'light' : 'dark';
    try { localStorage.setItem('hl-theme', root.dataset.theme); } catch {}
  });
}

// ---- 설명 따라가기(안내 투어) ----
const TOUR = [
  { preset: 'ok-none', hl: ['.controls'], title: '인터넷이 없는 서버에 새 버전을 올리는 일',
    text: ['은행이나 공공기관의 서버는 인터넷에 연결되어 있지 않은 경우가 많습니다. 담당자가 설치 파일 묶음(번들)을 들고 들어가 직접 올립니다.', '올리다 잘못되면 서비스가 멈춥니다. 그래서 도구는 무엇을 먼저 확인하고, 실패하면 어떻게 할지 미리 정해 두었습니다. 이 화면에서 그 동작을 눌러 볼 수 있습니다.'],
    look: '위쪽 "상황 고르기"에서 지금 버전과 올릴 버전을 고릅니다.' },
  { preset: 'ok-none', hl: ['#timeline li:nth-child(1)', '#timeline li:nth-child(2)'], title: '올리기 전에 두 가지를 먼저 봅니다',
    text: ['먼저 파일이 진짜인지 봅니다. 번들에는 전자서명과 파일별 지문(체크섬)이 있어서, 파일이 바뀌었으면 알 수 있습니다(verify).', '그다음 올려도 되는 버전인지 봅니다. 번들마다 "이 버전 이상에서만 올릴 수 있다"는 조건이 있습니다(gate).', '두 확인은 아무것도 바꾸기 전에 합니다.'],
    look: '진행 단계의 1번, 2번 칸입니다.' },
  { preset: 'ok-none', hl: ['#decision', '#timeline'], title: '문제가 없으면 이렇게 올라갑니다',
    text: ['확인이 끝나면 먼저 되돌릴 사본(백업)을 만듭니다. 그다음 새 버전을 올리고, 잘 뜨는지 봅니다. 잘 뜨면 성공입니다.', '이번 릴리스는 DB 구조를 바꾸지 않아서 "DB 구조 바꾸기" 칸은 건너뜁니다(SKIP).'],
    look: '초록색 OK 칸이 이어지고, 결과에 "새 버전으로 올렸고, 잘 동작합니다"가 나옵니다.' },
  { preset: 'health', hl: ['#decision', '#timeline li:nth-child(7)', '#timeline li:nth-child(8)'], title: '새 버전이 안 뜨면 자동으로 되돌립니다',
    text: ['새 버전이 제대로 뜨지 않으면 도구가 이전 버전으로 자동으로 되돌립니다(상태 점검 실패).', '되돌린 뒤에는 이전 버전이 잘 뜨는지 한 번 더 봅니다. 잘 떠야 복귀가 성공입니다. 한밤중에 사람이 불려 나오는 일을 줄이려는 동작입니다.'],
    look: '빨간 FAIL 칸 다음의 "이전 버전으로 복귀" 칸이 OK입니다.' },
  { preset: 'halt', hl: ['#decision', '#timeline li:nth-child(8)'], title: 'DB 구조가 바뀐 릴리스는 자동으로 되돌리지 못합니다',
    text: ['DB 구조(스키마)를 바꾼 릴리스는, 이전 버전 앱이 새 구조에서 동작하지 않을 수 있습니다.', '구조 변경을 두 번에 나눠서 이전 버전과도 맞게 만든 릴리스(compatible)는 실패해도 자동으로 되돌립니다. 먼저 추가만 하고(expand), 다음 릴리스에서 정리합니다(contract).', '나눌 수 없는 변경(breaking)이 실패하면 자동으로 되돌리지 않고 멈춥니다. 업그레이드 직전에 만든 백업으로 복구하도록 안내합니다.'],
    look: '"자동 롤백 불가", 결과는 "멈췄습니다", 마지막 칸은 SKIP입니다.' },
  { preset: 'sig', hl: ['#decision', '#timeline li:nth-child(1)', '#timeline li:nth-child(2)'], title: '바뀐 파일과 건너뛴 버전은 시작하지 않습니다',
    text: ['서명이나 파일이 맞지 않는 번들은 백업도 만들지 않고 거부합니다. 올릴 수 있는 최소 버전보다 낮은 버전에서 올리려 할 때도 같습니다.', '그래서 서버는 아무것도 바뀌지 않은 채 그대로입니다. "건너뛴 버전"과 "파일이 변조됨"도 눌러 보세요.'],
    look: '1번 칸이 FAIL이고, 나머지 칸은 실행하지 않습니다(—).' },
  { preset: 'mid', hl: ['.tabs'], title: '이 화면의 규칙은 실제 도구의 코드입니다',
    text: ['이 화면에서 올릴지, 되돌릴지, 멈출지 정하는 규칙은 실제 업그레이드 도구(harborctl)와 같은 Go 코드입니다. 브라우저에서 돌리려고 규칙을 따로 다시 만들지 않았습니다.', '아래 "도구를 실제로 실행한 화면"은 인터넷이 없는 테스트 환경에서 도구를 돌려 얻은 기록입니다. 상황을 바꿔 가며 화면의 결과와 비교해 보세요.'],
    look: '"실제로 돌려 본 기록" 패널의 터미널 화면입니다.' },
];
const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
const tourMs = Number(new URLSearchParams(location.search).get('tourms')) || 8000;
const tour = { open: false, i: 0, auto: false, timer: null };

function setHighlights(sels) {
  document.querySelectorAll('.tour-hl').forEach((e) => e.classList.remove('tour-hl'));
  for (const sel of sels || []) document.querySelectorAll(sel).forEach((e) => e.classList.add('tour-hl'));
}
function tourTimer() {
  clearTimeout(tour.timer);
  if (!tour.open || !tour.auto || reduced || tour.i >= TOUR.length - 1) return;
  tour.timer = setTimeout(() => tourGo(tour.i + 1), tourMs);
}
function tourGo(i, scroll = true) {
  tour.i = Math.max(0, Math.min(TOUR.length - 1, i));
  const st = TOUR[tour.i];
  const p = PRESETS.find((x) => x.id === st.preset);
  if (p) applyPreset(p);
  setHighlights(st.hl);
  $('tour-prog').textContent = `${tour.i + 1}/${TOUR.length}`;
  $('tour-prog').setAttribute('aria-label', `${TOUR.length}장 중 ${tour.i + 1}장`);
  $('tour-title').textContent = st.title;
  $('tour-text').replaceChildren(...st.text.map((t) => el('p', { textContent: t })));
  $('tour-look').textContent = `화면에서 볼 곳: ${st.look}`;
  $('tour-prev').disabled = tour.i === 0;
  $('tour-next').textContent = tour.i === TOUR.length - 1 ? '마치기' : '다음';
  if (scroll) {
    const first = document.querySelector('.tour-hl');
    if (first) first.scrollIntoView({ behavior: reduced ? 'auto' : 'smooth', block: 'start' });
  }
  tourTimer();
}
function tourOpen(i = tour.i, focus = false, scroll = true) {
  tour.open = true;
  $('tour').hidden = false;
  $('tour-open').hidden = true;
  document.body.classList.add('tour-open');
  syncSheet();
  tourGo(i, scroll);
  if (focus) $('tour-title').focus({ preventScroll: true });
}
function tourCollapse() {
  tourRemember();
  tour.open = false;
  clearTimeout(tour.timer);
  $('tour').hidden = true;
  $('tour-open').hidden = false;
  document.body.classList.remove('tour-open');
  document.documentElement.style.removeProperty('--sheet-h');
}
// 설명을 봤는지만 쿠키 하나에 기억한다(식별 정보 없음). 쿠키를 못 쓰면 매번 처음 방문처럼 동작한다.
const COOKIE = 'hl_tour';
function tourSeen() {
  try { return document.cookie.split('; ').some((c) => c === `${COOKIE}=seen`); } catch { return false; }
}
function tourRemember() {
  try {
    const dir = location.pathname.replace(/[^/]*$/, '') || '/';
    document.cookie = `${COOKIE}=seen; path=${dir}; max-age=15552000; SameSite=Lax`;
  } catch { /* 쿠키 차단: 기억하지 못할 뿐 동작은 같다 */ }
}
function tourFinish() {
  tourRemember();
  tour.i = 0;
  setHighlights([]);
  tourCollapse();
}
function syncSheet() {
  if (tour.open) document.documentElement.style.setProperty('--sheet-h', `${$('tour').offsetHeight}px`);
}
function setupTour() {
  $('tour-start').addEventListener('click', () => { tour.i = 0; tourOpen(0, true); });
  $('tour-skip').addEventListener('click', () => { tourFinish(); $('h-scn').scrollIntoView({ behavior: reduced ? 'auto' : 'smooth' }); });
  $('tour-open').addEventListener('click', () => tourOpen(tour.i, true));
  $('tour-collapse').addEventListener('click', () => { tourCollapse(); $('tour-open').focus(); });
  $('tour-prev').addEventListener('click', () => tourGo(tour.i - 1));
  $('tour-next').addEventListener('click', () => (tour.i === TOUR.length - 1 ? tourFinish() : tourGo(tour.i + 1)));
  const auto = $('tour-auto');
  if (reduced) {
    auto.disabled = true;
    auto.title = '동작 줄이기 설정이 켜져 있어 자동 진행을 쓰지 않습니다';
    $('tour-keys').textContent = '← → 키로 넘길 수 있습니다. (동작 줄이기 설정으로 자동 진행은 꺼져 있습니다)';
  }
  auto.addEventListener('click', () => {
    tour.auto = !tour.auto;
    auto.setAttribute('aria-pressed', String(tour.auto));
    auto.textContent = tour.auto ? '자동 진행 끄기' : '자동 진행 켜기';
    tourTimer();
  });
  document.addEventListener('keydown', (e) => {
    if (!tour.open || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return;
    if (e.target.closest && e.target.closest('select,textarea,input,[role="tab"]')) return;
    if (e.key === 'ArrowRight') { if (tour.i < TOUR.length - 1) tourGo(tour.i + 1); e.preventDefault(); }
    if (e.key === 'ArrowLeft') { if (tour.i > 0) tourGo(tour.i - 1); e.preventDefault(); }
  });
  if (window.ResizeObserver) new ResizeObserver(syncSheet).observe($('tour'));
}

async function main() {
  setupTheme();
  setupTabs();
  setupTour();
  $('decision').append(el('p', { className: 'boot', textContent: '불러오는 중…' }));
  const [manifests, e2e, llm] = await Promise.all([loadJSON('data/manifests.json', []), loadJSON('data/e2e.json', {}), loadJSON('data/llm-compat.json', [])]);
  data.manifests = manifests;
  data.e2e = e2e;
  data.llm = llm;
  fillSelects();
  setupPresets();
  renderLLM();
  $('sql').value = SAMPLE_SQL;
  for (const id of ['current', 'target']) $(id).addEventListener('change', manual);
  for (const r of document.querySelectorAll('input[name="fail"]')) r.addEventListener('change', manual);
  $('rbBroken').addEventListener('change', manual);
  $('run').addEventListener('click', () => run(activePreset));
  for (const id of ['sql', 'lint-rel']) $(id).addEventListener('input', lint);
  $('lint-decl').addEventListener('change', lint);
  await bootWasm();
  document.body.dataset.ready = wasmReady ? 'true' : 'error';
  if (!wasmReady) { $('decision').textContent = '규칙 실행 코드를 불러오지 못했습니다.'; return; }
  applyPreset(PRESETS[2]);
  lint();
  if (!tourSeen()) tourOpen(0, false, false); // 저절로 열 때는 화면을 옮기지 않는다(첫 화면 보존)
  else $('tour-open').hidden = false;
}
main();
