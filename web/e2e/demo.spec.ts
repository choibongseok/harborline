import { expect, test, type Page } from '@playwright/test';

async function open(page: Page) {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(String(e)));
  await page.context().addCookies([{ name: 'hl_tour', value: 'seen', url: 'http://127.0.0.1:4173/harborline/' }]);
  await page.goto('./');
  await expect(page.locator('body[data-ready="true"]')).toBeVisible({ timeout: 15_000 });
  return errors;
}
const preset = (page: Page, name: string) => page.getByRole('button', { name, exact: false }).first();
const stage = (page: Page, id: string) => page.locator('#timeline li', { hasText: id });

test('판정 엔진(WebAssembly)이 뜨고 기본 시나리오 결과가 나오며 콘솔 오류가 없다', async ({ page }) => {
  const errors = await open(page);
  await expect(page.locator('#decision')).toContainText('자동 복귀');
  await expect(page.locator('#timeline li')).toHaveCount(8);
  await expect(page.locator('.banner')).toContainText('판정 로직만 실제 코드');
  expect(errors).toEqual([]);
});

test('정상 업그레이드: 스키마 변경이 없으면 마이그레이션을 건너뛰고 성공한다', async ({ page }) => {
  await open(page);
  await preset(page, '정상적으로 올라감').click();
  await expect(page.locator('#decision')).toContainText('업그레이드 성공');
  await expect(page.locator('#decision')).toContainText('종료 코드 0');
  await expect(stage(page, 'migrate').locator('.chip')).toHaveText('SKIP');
  await expect(stage(page, 'health').locator('.chip')).toHaveText('OK');
  await expect(stage(page, 'rollback').locator('.chip')).toHaveText('—');
});

test('상태 점검 실패: 스키마 변경이 없으면 이전 버전으로 자동 복귀한다', async ({ page }) => {
  await open(page);
  await preset(page, '새 버전이 안 뜸').click();
  await expect(page.locator('#decision')).toContainText('자동 복귀');
  await expect(page.locator('#decision')).toContainText('종료 코드 4');
  await expect(stage(page, 'health').locator('.chip')).toHaveText('FAIL');
  await expect(stage(page, 'rollback').locator('.chip')).toHaveText('OK');
  await expect(stage(page, 'rollback')).toContainText('상태 점검 통과');
});

test('호환되지 않는 스키마 변경이 실패하면 자동 복귀 없이 정지하고 복구를 안내한다', async ({ page }) => {
  await open(page);
  await preset(page, 'DB 구조가 바뀜 + 실패').click();
  await expect(page.locator('#decision')).toContainText('정지 후 복구');
  await expect(page.locator('#decision')).toContainText('종료 코드 5');
  await expect(page.locator('#decision')).toContainText('harborctl restore');
  await expect(page.locator('#decision')).toContainText('자동 롤백 불가');
  await expect(stage(page, 'rollback').locator('.chip')).toHaveText('SKIP');
});

test('건너뛴 버전은 게이트에서 거부되고 백업도 만들지 않는다', async ({ page }) => {
  await open(page);
  await preset(page, '건너뛴 버전').click();
  await expect(page.locator('#decision')).toContainText('거부');
  await expect(stage(page, 'gate').locator('.chip')).toHaveText('FAIL');
  await expect(stage(page, 'gate')).toContainText('건너뛴 버전');
  await expect(stage(page, 'backup').locator('.chip')).toHaveText('—');
});

test('서명 불일치와 이미지 변조는 검증 단계에서 거부된다', async ({ page }) => {
  await open(page);
  await preset(page, '서명이 맞지 않음').click();
  await expect(stage(page, 'verify')).toContainText('서명이 맞지 않음');
  await expect(page.locator('#decision')).toContainText('종료 코드 2');
  await preset(page, '파일이 변조됨').click();
  await expect(stage(page, 'verify')).toContainText('체크섬이 맞지 않음');
});

test('직접 고른 버전과 실패 주입이 판정에 반영되고 시나리오 선택 표시가 풀린다', async ({ page }) => {
  await open(page);
  await preset(page, '정상적으로 올라감').click();
  await expect(preset(page, '정상적으로 올라감')).toHaveAttribute('aria-pressed', 'true');
  await page.locator('#current').selectOption('v1.2.0');
  await page.locator('#target').selectOption('v2.0.0');
  await page.getByLabel('마이그레이션 실패').check();
  await expect(preset(page, '정상적으로 올라감')).toHaveAttribute('aria-pressed', 'false');
  await expect(page.locator('#decision')).toContainText('정지 후 복구');
  await expect(stage(page, 'migrate').locator('.chip')).toHaveText('FAIL');
  await page.getByLabel('없음', { exact: true }).check();
  await page.locator('#target').selectOption('v1.1.0');
  await page.locator('#current').selectOption('v1.0.0');
  await expect(page.locator('#decision')).toContainText('업그레이드 성공');
});

test('실제 e2e 터미널 출력 탭이 해당 시나리오의 harborctl 실행 기록을 보여 준다', async ({ page }) => {
  await open(page);
  await preset(page, '새 버전이 안 뜸').click();
  await expect(page.locator('#p-term')).toContainText('harborctl upgrade --bundle ./harborline-v1.1.1');
  await expect(page.locator('#p-term')).toContainText('이전 버전으로 복귀, 상태 점검 통과');
  await expect(page.locator('#p-term')).toContainText('(종료 코드 4)');
  await preset(page, 'DB 구조가 바뀜 + 실패').click();
  await expect(page.locator('#p-term')).toContainText('harborctl restore');
});

test('LLM 호환성 표가 실제 실행 결과 파일의 모든 프로필을 보여 준다', async ({ page }) => {
  await open(page);
  await page.getByRole('tab', { name: 'LLM 호환성 표' }).click();
  await expect(page.locator('#llm tbody tr')).toHaveCount(6);
  await expect(page.locator('#llm')).toContainText('prompt_json');
  await expect(page.locator('#llm')).toContainText('chunked');
  await expect(page.locator('#llm tbody tr', { hasText: 'unreachable' })).toContainText('비활성');
});

test('마이그레이션 규약 검사기가 위반을 잡고 고치면 통과한다', async ({ page }) => {
  await open(page);
  await page.getByRole('tab', { name: '마이그레이션 규약 검사' }).click();
  await expect(page.locator('#lint-out')).toContainText('expand-drop-column');
  await expect(page.locator('#lint-out')).toContainText('expand-add-not-null-without-default');
  await page.locator('#sql').fill('-- release: v1.3.0\n-- phase: expand\nALTER TABLE inquiries ADD COLUMN channel text NOT NULL DEFAULT \'web\';\n');
  await expect(page.locator('#lint-out')).toContainText('규약 통과');
  await page.locator('#sql').fill('ALTER TABLE a ADD COLUMN b int;');
  await expect(page.locator('#lint-out')).toContainText('release/phase 헤더가 필요함');
});

test('키보드: 탭을 화살표로 옮기고 포커스 윤곽이 보이며 테마 전환이 동작한다', async ({ page }) => {
  await open(page);
  await page.getByRole('tab', { name: '실제 e2e 터미널 출력' }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.getByRole('tab', { name: 'LLM 호환성 표' })).toBeFocused();
  await expect(page.locator('#p-llm')).toBeVisible();
  const outline = await page.getByRole('tab', { name: 'LLM 호환성 표' }).evaluate((e) => getComputedStyle(e).outlineStyle);
  expect(outline).not.toBe('none');
  expect(await page.locator('html').getAttribute('data-theme')).toBeTruthy();
  const before = await page.locator('html').getAttribute('data-theme');
  await page.locator('#theme').click();
  expect(await page.locator('html').getAttribute('data-theme')).not.toBe(before);
});

test('모바일 폭에서 한 열로 쌓이고 가로 스크롤이 생기지 않는다', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await open(page);
  const w = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  expect(w.sw).toBeLessThanOrEqual(w.cw);
  const boxes = await page.evaluate(() => ['.controls', '.stage', '.tabs'].map((s) => { const r = document.querySelector(s)!.getBoundingClientRect(); return [r.left, r.top]; }));
  expect(boxes[0][0]).toBe(boxes[1][0]);
  expect(boxes[1][1]).toBeGreaterThan(boxes[0][1]);
});

test('외부 요청이 없고 /harborline/ 아래 모든 파일이 정상 응답한다', async ({ page }) => {
  const bad: string[] = [];
  const seen: string[] = [];
  page.on('response', (r) => { seen.push(r.url()); if (r.status() >= 400) bad.push(`${r.status()} ${r.url()}`); });
  await open(page);
  expect(bad).toEqual([]);
  expect(seen.length).toBeGreaterThan(5);
  for (const u of seen) {
    expect(u.startsWith('http://127.0.0.1:4173/harborline/')).toBe(true);
  }
});
