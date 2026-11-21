import { expect, test, type Page } from '@playwright/test';

async function open(page: Page, query = '', seen = true) {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  page.on('pageerror', (e) => errors.push(String(e)));
  if (seen) await page.context().addCookies([{ name: 'hl_tour', value: 'seen', url: 'http://127.0.0.1:4173/harborline/' }]);
  await page.goto(`./${query}`);
  await expect(page.locator('body[data-ready="true"]')).toBeVisible({ timeout: 15_000 });
  return errors;
}
const tour = (page: Page) => page.locator('#tour');
const start = (page: Page) => page.getByRole('button', { name: '설명 따라가기' });

test('첫 화면에 한 문장 설명과 두 버튼이 있고, 이미 본 방문자에게는 설명 패널이 닫혀 있다', async ({ page }) => {
  await open(page);
  await expect(page.locator('h1')).toContainText('잘못되면 어떻게 되돌리는지 보여 주는 데모');
  await expect(start(page)).toBeVisible();
  await expect(page.getByRole('button', { name: '직접 해 보기' })).toBeVisible();
  await expect(tour(page)).toBeHidden();
  await expect(page.locator('#glossary summary')).toHaveText('어려운 말 풀이');
});

test('설명 따라가기: 장을 넘기면 해당 시나리오가 자동 실행되고 볼 곳이 강조된다', async ({ page }) => {
  const errors = await open(page);
  await start(page).click();
  await expect(tour(page)).toBeVisible();
  await expect(page.locator('#tour-prog')).toHaveText('1/7');
  await expect(page.locator('.controls')).toHaveClass(/tour-hl/);
  await page.getByRole('button', { name: '다음', exact: true }).click();
  await page.getByRole('button', { name: '다음', exact: true }).click();
  await expect(page.locator('#tour-prog')).toHaveText('3/7');
  await expect(page.locator('#decision')).toContainText('새 버전으로 올렸고, 잘 동작합니다');
  await page.getByRole('button', { name: '다음', exact: true }).click();
  await expect(page.locator('#tour-title')).toContainText('새 버전이 안 뜨면 자동으로 되돌립니다');
  await expect(page.locator('#decision')).toContainText('이전 버전으로 자동으로 되돌렸습니다');
  await expect(page.locator('#timeline li', { hasText: 'health' })).toHaveClass(/tour-hl/);
  await page.getByRole('button', { name: '다음', exact: true }).click();
  await expect(page.locator('#decision')).toContainText('백업으로 복구해야 합니다');
  await expect(page.locator('#decision')).toContainText('자동 롤백 불가');
  await page.getByRole('button', { name: '다음', exact: true }).click();
  await expect(page.locator('#decision')).toContainText('시작하지 않았습니다');
  await page.getByRole('button', { name: '다음', exact: true }).click();
  await expect(page.locator('#tour-title')).toContainText('진짜 코드');
  await expect(page.locator('#tour-next')).toHaveText('마치기');
  await page.locator('#tour-next').click();
  await expect(tour(page)).toBeHidden();
  await expect(page.locator('.tour-hl')).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('설명 따라가기: 좌우 화살표 키로 장을 넘기고 진행 표시가 바뀐다', async ({ page }) => {
  await open(page);
  await start(page).click();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('#tour-prog')).toHaveText('2/7');
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('#tour-prog')).toHaveText('3/7');
  await page.keyboard.press('ArrowLeft');
  await expect(page.locator('#tour-prog')).toHaveText('2/7');
  await expect(page.locator('#tour-prev')).toBeEnabled();
});

test('설명 따라가기: 접으면 다시 여는 버튼이 보이고 펼치면 같은 장에서 이어진다', async ({ page }) => {
  await open(page);
  await start(page).click();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await page.getByRole('button', { name: '접기' }).click();
  await expect(tour(page)).toBeHidden();
  await expect(page.getByRole('button', { name: '설명 보기' })).toBeVisible();
  await page.getByRole('button', { name: '설명 보기' }).click();
  await expect(tour(page)).toBeVisible();
  await expect(page.locator('#tour-prog')).toHaveText('4/7');
});

test('설명 따라가기: 자동 진행을 켜면 다음 장으로 넘어가고 끄면 멈춘다', async ({ page }) => {
  await open(page, '?tourms=900');
  await start(page).click();
  await page.locator('#tour-auto').click();
  await expect(page.locator('#tour-auto')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#tour-prog')).not.toHaveText('1/7', { timeout: 5000 });
  await page.locator('#tour-auto').click();
  await expect(page.locator('#tour-auto')).toHaveAttribute('aria-pressed', 'false');
  const at = await page.locator('#tour-prog').textContent();
  await page.waitForTimeout(2200);
  await expect(page.locator('#tour-prog')).toHaveText(at!);
});

test('동작 줄이기 설정에서는 자동 진행을 쓰지 않는다', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await open(page);
  await start(page).click();
  await expect(page.locator('#tour-auto')).toBeDisabled();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('#tour-prog')).toHaveText('2/7');
});

test('모바일 폭에서 설명 시트가 본문 끝과 강조된 곳을 가리지 않는다', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await open(page);
  await start(page).click();
  await page.getByRole('button', { name: '다음', exact: true }).click();
  const sheetTop = () => page.locator('#tour').evaluate((e) => e.getBoundingClientRect().top);
  const hlTop = () => page.locator('.tour-hl').first().evaluate((e) => e.getBoundingClientRect().top);
  await expect.poll(async () => { const t = await hlTop(); return t >= 0 && t < (await sheetTop()); }, { timeout: 5000 }).toBe(true);
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  const footer = await page.locator('footer').evaluate((e) => e.getBoundingClientRect().bottom);
  expect(footer).toBeLessThanOrEqual(await sheetTop() + 1);
  const w = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  expect(w.sw).toBeLessThanOrEqual(w.cw);
});

test('처음 온 방문자에게는 설명 패널이 1장부터 저절로 열리고 포커스를 빼앗지 않는다', async ({ page }) => {
  const errors = await open(page, '', false);
  await expect(tour(page)).toBeVisible();
  await expect(page.locator('#tour-prog')).toHaveText('1/7');
  await expect(page.locator('#tour-title')).toContainText('인터넷이 없는 서버에 새 버전을 올리는 일');
  await expect(page.locator('.controls')).toHaveClass(/tour-hl/);
  const active = await page.evaluate(() => document.activeElement?.tagName);
  expect(active).toBe('BODY');
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  expect(errors).toEqual([]);
});

test('설명을 접은 뒤 새로고침하면 저절로 열리지 않고, 버튼으로 다시 열 수 있다', async ({ page }) => {
  await open(page, '', false);
  await expect(tour(page)).toBeVisible();
  await page.getByRole('button', { name: '접기' }).click();
  await page.reload();
  await expect(page.locator('body[data-ready="true"]')).toBeVisible({ timeout: 15_000 });
  await expect(tour(page)).toBeHidden();
  const cookies = await page.context().cookies();
  const c = cookies.find((x) => x.name === 'hl_tour');
  expect(c?.value).toBe('seen');
  expect(c?.path).toBe('/harborline/');
  expect(c?.sameSite).toBe('Lax');
  await page.getByRole('button', { name: '설명 보기' }).click();
  await expect(tour(page)).toBeVisible();
});

test('설명을 끝까지 본 뒤 새로고침하면 저절로 열리지 않는다', async ({ page }) => {
  await open(page, '', false);
  for (let i = 0; i < 6; i++) await page.getByRole('button', { name: '다음', exact: true }).click();
  await page.locator('#tour-next').click();
  await expect(tour(page)).toBeHidden();
  await page.reload();
  await expect(page.locator('body[data-ready="true"]')).toBeVisible({ timeout: 15_000 });
  await expect(tour(page)).toBeHidden();
});

test('쿠키를 쓸 수 없어도 오류 없이 저절로 열린다', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(Document.prototype, 'cookie', { get() { throw new Error('blocked'); }, set() { throw new Error('blocked'); } });
  });
  const errors = await open(page, '', false);
  await expect(tour(page)).toBeVisible();
  await page.getByRole('button', { name: '접기' }).click();
  await page.reload();
  await expect(page.locator('body[data-ready="true"]')).toBeVisible({ timeout: 15_000 });
  await expect(tour(page)).toBeVisible();
  expect(errors).toEqual([]);
});

test('모바일 폭에서 저절로 열린 설명 시트가 첫 화면의 한 문장을 가리지 않는다', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await open(page, '', false);
  await expect(tour(page)).toBeVisible();
  await page.waitForTimeout(600);
  const r = await page.evaluate(() => ({ h1: document.querySelector('h1')!.getBoundingClientRect().bottom, sheet: document.getElementById('tour')!.getBoundingClientRect().top }));
  expect(r.h1).toBeLessThan(r.sheet);
});
