// Run separately from API load: node frontend/scripts/perf-browser.mjs config.json
import { chromium } from 'playwright';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import path from 'node:path';

const config = JSON.parse(await readFile(process.argv[2], 'utf8'));
const base = new URL(config.baseUrl);
if (base.protocol !== 'https:' || base.username || base.password || base.pathname !== '/' || base.search || base.hash) throw new Error('Use the dedicated deployment HTTPS origin');
if (config.localSelfSigned && !['localhost', '127.0.0.1', '[::1]'].includes(base.hostname)) throw new Error('Self-signed certificate support is restricted to loopback origins');
if (!config.unavailable || !config.core?.length || !config.completed || !config.recovered) throw new Error('Configure unavailable, core actions, completion, and recovered assertions');
if (config.phase === 'recovery' && config.routes?.length) throw new Error('Recovery must restore optional dependency requests');
const out = path.resolve(config.out);
await mkdir(out, { recursive: true, mode: 0o700 });
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, ignoreHTTPSErrors: config.localSelfSigned === true });
const sessions = JSON.parse(await readFile(config.sessions, 'utf8'));
const account = sessions[config.accountIndex || 0];
await context.addCookies([{ name: 'foc-refresh-token', value: account.refreshCookie,
  domain: base.hostname, path: '/user.v1.AuthService/', httpOnly: true, secure: true, sameSite: 'Strict' }]);
const page = await context.newPage();
page.setDefaultTimeout(config.timeoutMs || 15000);
const evidence = { feature: config.feature, mode: config.mode, phase: config.phase || 'outage',
  startedAt: new Date().toISOString(), status: 'fail', screenshots: [], actions: [], injectedRequests: 0 };

function locator(step) {
  if (step.selector) return page.locator(step.selector);
  if (step.label) return page.getByLabel(step.label, { exact: true });
  if (step.role) return page.getByRole(step.role, { name: step.name, exact: true });
  if (step.text) return page.getByText(step.text, { exact: true });
  throw new Error('Action requires selector, label, role/name, or text');
}

async function visible(step) {
  const element = locator(step);
  await element.waitFor({ state: 'visible' });
  if (step.enabled && !await element.isEnabled()) throw new Error('Core control is disabled');
}

async function actions(steps) {
  for (const step of steps || []) {
    if (step.op === 'goto') await page.goto(new URL(step.path, base).href);
    else if (step.op === 'click') await locator(step).click();
    else if (step.op === 'fill') await locator(step).fill(step.value);
    else if (step.op === 'press') await locator(step).press(step.key);
    else if (step.op === 'select') await locator(step).selectOption(step.value);
    else if (step.op === 'visible') await visible(step);
    else if (step.op === 'url') await page.waitForURL(new URL(step.path, base).href);
    else throw new Error(`Unknown browser action ${step.op}`);
    evidence.actions.push({ op: step.op, at: new Date().toISOString() });
  }
}

try {
  for (const route of config.routes || []) {
    await context.route(route.url, async (request) => {
      evidence.injectedRequests++;
      if (config.mode === 'connection_failure') await request.abort('connectionrefused');
      else if (config.mode === 'server_error') await request.fulfill({ status: 503, body: 'Controlled optional dependency outage' });
      else if (config.mode === 'timeout') {
        await new Promise((resolve) => setTimeout(resolve, config.timeoutMs || 15000));
        await request.abort('timedout');
      } else throw new Error('Unsupported browser fault mode');
    });
  }
  await page.goto(new URL(config.path || '/orders/new', base).href);
  await actions(config.setup);
  if (evidence.phase === 'recovery') {
    await visible(config.recovered);
  } else {
    await visible(config.unavailable);
    if (config.routes?.length && !evidence.injectedRequests) throw new Error('No optional dependency request hit the configured fault');
  }
  await actions(config.core);
  await visible(config.completed);
  evidence.status = 'pass';
} catch (error) {
  // Error messages can include input values. Keep only the class in shareable results.
  evidence.error = error.constructor.name;
} finally {
  const filename = `${evidence.phase}-${evidence.status}.png`;
  await page.screenshot({ path: path.join(out, filename), fullPage: true });
  evidence.screenshots.push(filename);
  // Persist rotating cookies for the same exclusive account across phases.
  const cookie = (await context.cookies()).find((cookie) => cookie.name === 'foc-refresh-token');
  if (cookie) {
    account.refreshCookie = cookie.value;
    await writeFile(config.sessions, JSON.stringify(sessions, null, 2), { mode: 0o600 });
  }
  evidence.finishedAt = new Date().toISOString();
  await writeFile(path.join(out, `browser-${evidence.phase}.json`), JSON.stringify(evidence, null, 2), { mode: 0o600 });
  await context.close();
  await browser.close();
}
if (evidence.status !== 'pass') process.exitCode = 1;
