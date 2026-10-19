// Main user journey (issue #107). Not a repo dependency: paste this function into the
// Playwright MCP tool `browser_run_code_unsafe` (it receives `page`), after setting the
// three constants below. Steps and expected results: _docs/e2e.md.
//   BASE   the Vite URL of the stack under test (see _docs/e2e.md, "Start the stacks")
//   STACK  label used in the screenshot folder (sqlite | postgres | mock)
//   SHOTS  folder for screenshots; keep it OUTSIDE git (the folder must exist)
//   (STACK='mock' navigates client-side only: the mock store is in memory)
//   LAYOUT true: also run the 320/768/1280 x light/dark pass after step 9
async (page) => {
  const BASE = 'http://127.0.0.1:5199', STACK = 'sqlite', SHOTS = '/tmp/mg-e2e-shots', LAYOUT = true;
  const B = BASE, SD = `${SHOTS}/${STACK}/`, MOCK = STACK === 'mock';
  // The mock keeps its data in memory, so a full page load wipes it: in mock mode every
  // navigation after the first load is client-side (history.pushState + popstate).
  let loaded = false;
  const nav = async u => {
    if (MOCK && loaded) { const p = u.replace(B, '') || '/'; await page.evaluate(p => { history.pushState({}, '', p); dispatchEvent(new PopStateEvent('popstate')); }, p); await page.waitForTimeout(250); return; }
    loaded = true; await page.goto(u);
  };
  await page.context().clearCookies();
  const tag = String(Date.now() % 100000), A = 'alice' + tag, C = 'bob' + tag, P = 'e2e-pass-12345';
  const R = [], errs = new Set(), overflow = [];
  page.on('console', m => { if (m.type() === 'error') errs.add(page.url().replace(B, '') + ' ' + m.text().slice(0, 120)); });
  page.on('pageerror', e => errs.add('PAGEERROR ' + e.message));
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.emulateMedia({ colorScheme: 'light' });
  page.setDefaultTimeout(8000);
  const shot = n => page.screenshot({ path: SD + n + '.png', fullPage: true });
  const step = async (n, fn) => { try { R.push([n, 'PASS', (await fn()) || '']); } catch (e) { R.push([n, 'FAIL', e.message.split('\n')[0]]); await shot('FAIL-' + n.replace(/\W+/g, '_')).catch(() => {}); } };
  const must = (c, m) => { if (!c) throw new Error(m); };
  const settle = () => page.waitForLoadState('networkidle').catch(() => {});
  const login = async u => { await nav(B + '/login'); await page.fill('input[name=username]', u); await page.fill('input[name=password]', P); await page.click('button[type=submit]'); await page.getByRole('button', { name: 'Log out' }).waitFor(); };
  const logout = async () => { await page.getByRole('button', { name: 'Log out' }).click(); await page.getByRole('link', { name: 'Log in' }).first().waitFor(); };
  const commit = async (branch, path, content, msg) => { await nav(`${B}/${A}/demo/new/${branch}`); await page.getByLabel('File path').fill(path); await page.getByLabel('File content').fill(content); await page.getByLabel('Commit message').fill(msg); await page.getByRole('button', { name: 'Commit', exact: true }).click(); await page.waitForURL(/\/commit\//); };

  await step('1 register', async () => { await nav(B + '/'); await shot('01-home-anon'); await page.getByRole('link', { name: 'Register' }).click(); await page.fill('input[name=username]', A); await page.fill('input[name=password]', P); await shot('02-register'); await page.click('button[type=submit]'); await page.getByRole('button', { name: 'Log out' }).waitFor(); return 'auto-logged-in, url=' + page.url().replace(B, ''); });
  await step('2 logout + login', async () => { await logout(); await login(A); await shot('03-home-loggedin'); });
  await step('3 create repo', async () => { await page.getByRole('button', { name: 'New repository' }).click(); await page.getByLabel('Name', { exact: true }).fill('demo'); await page.getByLabel('Description (optional)').fill('e2e demo'); await shot('04-new-repo'); await page.getByRole('button', { name: 'Create repository' }).click(); await page.getByRole('link', { name: A + '/demo' }).waitFor(); });
  await step('4 commit README in editor', async () => { await commit('main', 'README.md', '# demo\nhello\n', 'first commit'); await page.getByText('+2 -0').first().waitFor(); await shot('05-commit-diff'); });
  await step('5 browse tree/file/history/diff', async () => {
    await nav(`${B}/${A}/demo`); await page.getByRole('link', { name: 'README.md' }).first().waitFor(); await shot('06-repo-tree'); await page.getByText('hello').first().waitFor();
    await page.getByRole('link', { name: 'README.md' }).first().click(); await page.waitForURL(/\/blob\//); await page.getByText('hello').first().waitFor(); await shot('07-blob');
    await nav(`${B}/${A}/demo/commits/main`); await page.getByText('first commit').first().waitFor(); await shot('08-history');
    await page.locator('a[href*="/commit/"]').first().click(); await page.waitForURL(/\/commit\//); await page.getByText('+2 -0').first().waitFor(); await shot('09-diff'); });
  await step('6 create branch', async () => { await nav(`${B}/${A}/demo`); await page.getByRole('button', { name: 'Create branch' }).click(); const d = page.getByRole('dialog'); await d.getByLabel('Branch name').fill('feature'); await shot('10-create-branch'); await d.getByRole('button', { name: 'Create branch' }).click(); await page.waitForURL(/tree\/feature/); });
  await step('7 commit on branch', async () => { await commit('feature', 'docs/notes.txt', 'feature work\n', 'feature commit'); });
  await step('8 compare shows diff', async () => { await nav(`${B}/${A}/demo/compare?base=main&head=feature`); await page.getByText('docs/notes.txt').first().waitFor(); await shot('11-compare'); });
  await step('9 merge feature into main', async () => { await nav(`${B}/${A}/demo`); await page.getByRole('button', { name: 'Merge', exact: true }).click(); await shot('12-merge-dialog'); await page.getByRole('alertdialog').getByRole('button', { name: 'Merge', exact: true }).click(); await page.waitForTimeout(1500); await nav(`${B}/${A}/demo/tree/main`); await page.getByRole('link', { name: 'docs' }).first().waitFor(); await shot('13-after-merge'); });
  if (LAYOUT) await step('L layout 320/768/1280 x light/dark', async () => {
    const urls = { home: '/', repo: `/${A}/demo`, blob: `/${A}/demo/blob/main/README.md`, history: `/${A}/demo/commits/main`, compare: `/${A}/demo/compare?base=main&head=feature`, editor: `/${A}/demo/new/main`, settings: `/${A}/demo/settings`, profile: `/${A}` };
    for (const scheme of ['light', 'dark']) for (const w of [320, 768, 1280]) {
      await page.emulateMedia({ colorScheme: scheme }); await page.setViewportSize({ width: w, height: 800 });
      for (const [k, u] of Object.entries(urls)) {
        await nav(B + u); await settle(); await page.waitForTimeout(400);
        const o = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        if (o > 0) overflow.push(`${k}@${w}/${scheme}: +${o}px`);
        await shot(`L-${k}-${w}-${scheme}`);
      }
    }
    await page.emulateMedia({ colorScheme: 'light' }); await page.setViewportSize({ width: 1280, height: 800 });
    must(!overflow.length, 'horizontal overflow: ' + overflow.join('; ')); });
  await step('10 toggle private', async () => { await nav(`${B}/${A}/demo/settings`); await page.getByRole('button', { name: 'Change visibility' }).click(); await shot('14-visibility-dialog'); await page.getByRole('button', { name: 'Make private' }).click(); await page.getByText('currently private').waitFor(); await shot('15-settings-private'); });
  await step('11 logout', async () => { await logout(); await shot('16-logged-out'); });
  await step('12 anonymous sees private repo as not found', async () => { await nav(`${B}/${A}/demo`); await page.getByText(/not found/i).first().waitFor(); await shot('17-anon-private'); });
  await step('13 register + login second user', async () => { await nav(B + '/register'); await page.fill('input[name=username]', C); await page.fill('input[name=password]', P); await page.click('button[type=submit]'); await page.waitForTimeout(1200); if (!(await page.getByRole('button', { name: 'Log out' }).isVisible())) await login(C); });
  await step('14 other user cannot see private repo', async () => { await nav(`${B}/${A}/demo`); await page.getByText(/not found/i).first().waitFor(); await shot('18-bob-private'); await nav(B + '/'); await settle(); await page.waitForTimeout(700); must(!(await page.locator(`main a[href="/${A}/demo"]`).count()), 'private repo listed to other user'); });
  await step('15 second user logout', async () => { await logout(); });
  await step('16 owner makes public again, second user sees it read-only', async () => { await login(A); await nav(`${B}/${A}/demo/settings`); await page.getByRole('button', { name: 'Change visibility' }).click(); await page.getByRole('button', { name: 'Make public' }).click(); await page.getByText('currently public').waitFor(); await logout(); await login(C); await nav(`${B}/${A}/demo`); await page.getByRole('link', { name: 'README.md' }).first().waitFor(); must(!(await page.getByRole('link', { name: 'Settings' }).count()), 'Settings tab visible to non-owner'); await shot('19-bob-public'); await logout(); });
  return { A, C, R, overflow, consoleErrors: [...errs].filter(e => !/status of 40[14]/.test(e)), ignored401_404: [...errs].filter(e => /status of 40[14]/.test(e)).length };
}
