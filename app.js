// 판정은 Go(WebAssembly)가 한다. 이 파일은 입력을 모아 넘기고 결과를 그린다. 규칙을 다시 구현하지 않는다.
const STAGES = [
  ['verify', '서명·체크섬', '번들이 변조되지 않았는지'],
  ['gate', '버전 게이트', '최소 기존 버전 확인'],
  ['backup', '백업', 'DB·설정·첨부'],
  ['load', '이미지 적재', 'docker load'],
  ['migrate', '마이그레이션', '스키마 변경'],
  ['switch', '교체', '새 버전으로 재기동'],
  ['health', '상태 점검', '새 버전이 응답하는지'],
  ['rollback', '이전 버전 복귀', '실패 시에만'],
];
const OUTCOME = {
  success: ['진행: 업그레이드 성공', 0],
  rejected: ['거부: 아무것도 바꾸지 않음', 2],
  aborted: ['중단: 운영 중인 버전 그대로', 3],
  rolled_back: ['자동 복귀: 이전 버전으로 돌아감', 4],
  halted: ['정지 후 복구: 자동 복귀 불가', 5],
};
const PRESETS = [
  { id: 'ok-none', label: '정상 · 스키마 변경 없음', cur: 'v1.0.0', tgt: 'v1.1.0', fail: 'none', real: 'TestUpgradeWithoutSchemaChangeKeepsData' },
  { id: 'ok-expand', label: '정상 · expand 마이그레이션', cur: 'v1.0.0', tgt: 'v1.2.0', fail: 'none', real: 'TestUpgradeWithExpandMigrationAddsColumnsAndKeepsData' },
  { id: 'health', label: '상태 점검 실패 → 자동 복귀', cur: 'v1.1.0', tgt: 'v1.1.1', fail: 'none', real: 'TestFailedHealthCheckWithoutSchemaChangeRollsBackAutomatically' },
  { id: 'mid', label: '마이그레이션 중간 실패', cur: 'v1.1.0', tgt: 'v1.2.1', fail: 'migrate', real: 'TestMigrationFailingMidwayKeepsPreviousVersionServing' },
  { id: 'halt', label: '호환 불가 스키마 + 실패 → 정지', cur: 'v1.2.0', tgt: 'v2.0.0', fail: 'health', real: 'TestBreakingSchemaFailureHaltsAndBackupRestoreRecovers' },
  { id: 'sig', label: '서명 불일치', cur: 'v1.2.0', tgt: 'v1.2.2', fail: 'badSignature', real: 'TestBundleWithWrongSignatureIsRejectedBeforeAnyChange' },
  { id: 'sum', label: '이미지 변조', cur: 'v1.2.0', tgt: 'v1.2.3', fail: 'badChecksum', real: 'TestBundleWithTamperedImageIsRejectedByChecksum' },
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

function render(res, inp, m) {
  const [label, code] = OUTCOME[res.outcome];
  const d = $('decision');
  d.dataset.o = res.outcome;
  d.replaceChildren(
    el('p', { className: 'what', textContent: label }),
    el('p', { className: 'meta', textContent: `harborctl 종료 코드 ${code} · ${inp.current || '신규 설치'} → ${m.version}` }),
  );
  if (res.advice) d.append(el('p', { className: 'advice', textContent: res.advice }));
  const facts = el('div', { className: 'facts' });
  const add = (k, v) => facts.append(el('span', {}, `${k} `, el('b', { textContent: v })));
  add('이후 버전', res.currentAfter || '없음');
  add('스키마 변경', res.schema);
  add('자동 롤백', res.rollbackOk ? '가능' : '불가');
  d.append(facts, el('p', { className: 'hint', textContent: `근거: ${res.rollbackWhy}` }));

  const byStage = new Map(res.events.map((e) => [e.stage, e]));
  const tl = $('timeline');
  tl.replaceChildren();
  STAGES.forEach(([id, name, sub], i) => {
    const ev = byStage.get(id);
    const li = el('li', { className: ev ? ev.status : 'none' });
    li.style.setProperty('--i', i);
    li.append(
      el('span', { className: 'n', textContent: String(i + 1).padStart(2, '0') }),
      el('span', { className: 'st' }, name, el('small', { textContent: id })),
      el('span', { className: `chip ${ev ? ev.status : 'none'}`, textContent: ev ? ev.status.toUpperCase() : '—' }),
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
    $('term-note').textContent = '이 조합은 e2e 에서 돌린 시나리오가 아닙니다. 위의 시나리오 버튼을 누르면 같은 상황을 실제로 실행한 터미널 기록이 나옵니다.';
    return;
  }
  $('term-note').textContent = `격리 네트워크(외부로 나가는 길 없음)에서 harborctl 을 실제로 실행한 출력입니다. 테스트: ${preset.real}`;
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
  if (r.violations.length === 0) { out.append(el('p', { className: 'okmsg', textContent: `규약 통과 · 계산된 분류: ${r.classified || 'none'}` })); return; }
  const ul = el('ul', { className: 'viol' });
  for (const v of r.violations) ul.append(el('li', { textContent: `[${v.rule}] ${v.msg}` }));
  out.append(el('p', { className: 'hint', textContent: `번들 빌드가 거부함 · 계산된 분류: ${r.classified || '-'}` }), ul);
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
  const pref = saved || 'light';
  root.dataset.theme = pref;
  $('theme').addEventListener('click', () => {
    root.dataset.theme = root.dataset.theme === 'dark' ? 'light' : 'dark';
    try { localStorage.setItem('hl-theme', root.dataset.theme); } catch {}
  });
}

async function main() {
  setupTheme();
  setupTabs();
  $('decision').append(el('p', { className: 'boot', textContent: '판정 엔진(WebAssembly)을 불러오는 중…' }));
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
  if (!wasmReady) { $('decision').textContent = '판정 엔진을 불러오지 못했습니다.'; return; }
  applyPreset(PRESETS[2]);
  lint();
}
main();
