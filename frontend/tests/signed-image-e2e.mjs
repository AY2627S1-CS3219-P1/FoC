// Run with: node tests/signed-image-e2e.mjs
import assert from 'node:assert/strict';
import { createServer } from 'vite';
import { chromium } from 'playwright';
const server = await createServer({ server: { port: 0, fs: { allow: [process.cwd()] } }, plugins: [{
 name: 'signed-image-harness', configureServer(server) {
  server.middlewares.use('/signed-image-test', (_req, res) => {
   res.setHeader('Content-Type', 'text/html');
   res.end(`<body><script type="module" src="/tests/signed-image-harness.ts"></script></body>`);
  });
 }
}] });
await server.listen();
const browser = await chromium.launch({ headless: true });
const base = `http://localhost:${server.httpServer.address().port}`;
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64');
async function scenario({ initialOK = false, replacementOK = true, grantOK = true, viewer = false, lazy = false, action } = {}) {
 const page = await browser.newPage();
 page.setDefaultTimeout(5000);

 let grants = 0, images = 0, release;
 const pending = new Promise(resolve => release = resolve);
 const errors = [];
 page.on('pageerror', e => errors.push(e.message));
 await page.route(`${base}/image/**`, async route => {
  images++;
  const ok = route.request().url().endsWith('initial') ? initialOK : replacementOK;
  await route.fulfill(ok ? { contentType: 'image/png', body: png } : { status: 403 });
 });
 await page.route(`${base}/grant/**`, async route => {
  grants++;
  if (action && grants === 1) await pending;
  await route.fulfill(grantOK ? { json: { url: `${base}/image/replacement`, expiresAt: Date.now() + 60000 } } : { status: 403 });
 });
 await page.goto(`${base}/signed-image-test${viewer ? '?viewer' : lazy ? '?lazy' : ''}`);
 if (lazy) {
  await page.waitForTimeout(100);
  assert.equal(images, 0);
  assert.equal(grants, 0);
  await page.locator('img').scrollIntoViewIfNeeded();
 }
 if (action) {
  await page.waitForFunction(() => document.querySelector('[role="status"]'));
  await page.getByRole('button', { name: action }).click();
  release();
 }
 if (action === 'Unmount') {
  await page.waitForTimeout(100);
  assert.equal(await page.locator('img').count(), 0);
 } else if (!grantOK || !replacementOK) {
  await page.getByRole('img', { name: 'Test photo is unavailable' }).waitFor();
 } else {
  await page.waitForFunction(() => document.querySelector('img')?.naturalWidth === 1, null, { timeout: 5000 }).catch(async e => { console.log(await page.content(), errors, { grants, images }); throw e; });
 }
 await page.waitForTimeout(100);
 assert.deepEqual(errors, []);
 await page.close();
 return { grants, images };
}
async function viewerScenario() {
 const page = await browser.newPage();
 const photoId = '00000000-0000-4000-8000-000000000001';
 let refreshes = 0, grants = 0, imageReads = 0;
 const user = { id: '00000000-0000-4000-8000-000000000002', email: 'test@example.com', displayName: 'Test', role: 'USER_ROLE_USER' };
 const errors = [];
 page.on('pageerror', e => errors.push(e.message));
 await page.route('**/*', async route => {
  const request = route.request(), path = new URL(request.url()).pathname;
  if (!path.includes('Service/')) return route.fallback();
  const headers = { 'access-control-allow-origin': base, 'access-control-allow-credentials': 'true',
   'access-control-allow-headers': '*', 'access-control-allow-methods': 'POST,OPTIONS' };
  if (request.method() === 'OPTIONS') return route.fulfill({ status: 204, headers });
  let json = {};
  if (path.endsWith('AuthService/Refresh')) {
   refreshes++;
   json = { accessToken: `token-${refreshes}`, user };
  } else if (path.endsWith('ReviewService/GetReviewPhotoDownload')) {
   assert.equal(request.postDataJSON().photoId, photoId);
   assert.match(request.headers().authorization, /^Bearer token-/);
   grants++;
   if (grants === 1) return route.fulfill({ status: 401, headers, json: { code: 'unauthenticated', message: 'Expired token' } });
   json = { photo: { id: photoId, url: `${base}/image/viewer`, contentType: 'image/png', expiresAt: new Date(Date.now() + 60000).toISOString() } };
  } else if (path.endsWith('ProfileService/GetMyProfile')) json = { profile: user };
  return route.fulfill({ headers, json });
 });
 await page.route(`${base}/image/viewer`, async route => { imageReads++; await route.fulfill({ contentType: 'image/png', body: png }); });
 try {
  await page.goto(`${base}/reviews/photos/${photoId}`);
  await page.waitForFunction(() => document.querySelector('img[alt="Review photo"]')?.naturalWidth === 1);
  assert.equal(grants, 2);
  assert.equal(refreshes, 2); // restore session, then expired-token refresh
  await page.reload();
  await page.waitForFunction(() => document.querySelector('img[alt="Review photo"]')?.naturalWidth === 1);
  assert.equal(grants, 3);
  assert.equal(imageReads, 2);
  assert.deepEqual(errors, []);
 } finally { await page.close(); }
}
try {
 assert.deepEqual(await scenario({ initialOK: true }), { grants: 0, images: 1 });
 assert.deepEqual(await scenario({ lazy: true }), { grants: 1, images: 2 });
 assert.deepEqual(await scenario(), { grants: 1, images: 2 });
 assert.deepEqual(await scenario({ replacementOK: false }), { grants: 1, images: 2 });
 assert.deepEqual(await scenario({ grantOK: false }), { grants: 1, images: 1 });
 assert.deepEqual(await scenario({ viewer: true }), { grants: 1, images: 1 });
 assert.deepEqual(await scenario({ viewer: true, replacementOK: false }), { grants: 1, images: 1 });
 for (const action of ['Change image', 'Change session', 'Unmount']) {
  const result = await scenario({ action });
  assert.equal(result.grants, action === 'Unmount' ? 1 : 2);
  assert.equal(result.images, action === 'Unmount' ? 1 : 3);
 }
 await viewerScenario();
 console.log('SignedImage: 10 component scenarios and viewer open/reload with token refresh passed');
} finally { await browser.close(); await server.close(); }
