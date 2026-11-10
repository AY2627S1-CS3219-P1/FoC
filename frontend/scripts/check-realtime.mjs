// Browser integration with a real, long-lived Connect JSON stream. Domain RPCs
// use shared fixtures; PostgreSQL/Kafka delivery is tested separately in Go.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { chromium } from 'playwright';

const base = process.env.WIREFRAME_BASE_URL ?? 'http://localhost:5173';
const alice = '11111111-1111-4111-8111-111111111111';
const bob = '22222222-2222-4222-8222-222222222222';
const id = '33333333-3333-4333-8333-333333333333';
const offerId = '44444444-4444-4444-8444-444444444444';
const attemptId = '55555555-5555-4555-8555-555555555555';
const balances = new Map([[alice, '100'], [bob, '100']]);
const streams = new Map();
const openQueries = [];
const runtimeErrors = [];
let offers = [];
let available = true;
let accepts = 0, attemptStatus = 'ASSIGNMENT_ATTEMPT_STATUS_PENDING';
let order = {
 id, requesterId: alice, status: 'ORDER_STATUS_AWAITING_COURIER',
 itemDescription: 'Live update delivery', rewardCredits: '20', revision: '1',
 pickup: { locationId: offerId, name: 'Campus cafe', buildingName: 'COM2' },
 deliveryBuildingName: 'PGP', deliveryDuration: '1800s',
 createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 3_600_000).toISOString()
};
function frame(value, flags = 0) {
 const data = Buffer.from(JSON.stringify(value));
 const header = Buffer.alloc(5); header[0] = flags; header.writeUInt32BE(data.length, 1);
 return Buffer.concat([header, data]);
}
function notify(user, scopes) {
 for (const response of streams.get(user) ?? []) response.write(frame({ scopes }));
}
function orderChanged() {
 notify(alice, ['CHANGE_SCOPE_ORDERS', 'CHANGE_SCOPE_OPEN_ORDERS']);
 notify(bob, ['CHANGE_SCOPE_ORDERS', 'CHANGE_SCOPE_OPEN_ORDERS']);
}
function closeStreams(user) {
 for (const response of streams.get(user) ?? []) response.end(frame({ error: { code: 'unavailable', message: 'reconnect' } }, 2));
}

const server = createServer((request, response) => {
 response.setHeader('Access-Control-Allow-Origin', request.headers.origin ?? new URL(base).origin);
 response.setHeader('Access-Control-Allow-Credentials', 'true');
 response.setHeader('Access-Control-Allow-Headers', 'Authorization,Content-Type,Connect-Protocol-Version,Connect-Timeout-Ms');
 response.setHeader('Access-Control-Allow-Methods', 'POST,OPTIONS');
 if (request.method === 'OPTIONS') { response.writeHead(204); response.end(); return; }
 if (!request.url.endsWith('/WatchChanges')) { response.setHeader('Content-Type', 'application/json'); response.end('{}'); return; }
 if (!available) { response.writeHead(503, { 'Content-Type': 'application/json' }); response.end('{"code":"unavailable"}'); return; }
 const user = request.headers.authorization?.replace('Bearer ', '');
 response.writeHead(200, { 'Content-Type': 'application/connect+json' });
 if (!streams.has(user)) streams.set(user, new Set());
 streams.get(user).add(response);
 response.on('close', () => streams.get(user)?.delete(response));
 response.write(frame({ ready: true }));
});
await new Promise((resolve, reject) => { server.once('error', reject); server.listen(8084, '127.0.0.1', resolve); });
const browser = await chromium.launch();
async function open(user) {
 const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
 context.on('page', (page) => page.on('pageerror', (error) => runtimeErrors.push(error.message)));
 await context.route(/tile\.openstreetmap\.org/, (route) => route.abort());
 await context.route(/localhost:808[1235]\//, async (route) => {
  const method = new URL(route.request().url()).pathname.split('/').at(-1);
  const profile = { id: user, email: `${user === alice ? 'alice' : 'bob'}@example.test`, displayName: user === alice ? 'Alice' : 'Bob', role: 'USER_ROLE_USER' };
  const pageInfo = { page: 1, pageSize: 20, totalItems: 1, totalPages: 1 };
  let value = {};
  switch (method) {
   case 'Refresh': value = { accessToken: user, user: profile }; break;
   case 'GetMyProfile': value = { profile }; break;
   case 'GetMyAccount': value = { account: { userId: user, balance: balances.get(user), available: balances.get(user), reserved: '0' } }; break;
   case 'ListMyLedgerEntries': value = { entries: [], pageInfo }; break;
   case 'GetOrder': value = { order }; break;
   case 'ListMyOrders': value = { orders: [order], pageInfo }; break;
   case 'ListOpenOrders': openQueries.push(JSON.parse(route.request().postData() || '{}')); value = { orders: order.status === 'ORDER_STATUS_AWAITING_COURIER' ? [order] : [], pageInfo }; break;
   case 'ListOffers': value = { offers: user === alice ? offers : offers.filter((offer) => offer.courierId === user) }; break;
   case 'GetAssignmentAttempt': value = { attempt: { attemptId, status: attemptStatus } }; break;
   case 'AcceptOrder':
    accepts++;
    if (accepts === 1) { value = { order, attempt: { attemptId, status: attemptStatus } }; break; }
    order = { ...order, status: 'ORDER_STATUS_AWAITING_PICKUP', courierId: bob, revision: '2', assignedAt: new Date().toISOString() };
    value = { order, attempt: { attemptId, status: 'ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED' } }; orderChanged(); break;
   case 'ConfirmPickup':
    order = { ...order, status: 'ORDER_STATUS_DELIVERING', revision: '3', pickedUpAt: new Date().toISOString() }; orderChanged(); break;
   case 'ConfirmReceipt':
    order = { ...order, status: 'ORDER_STATUS_COMPLETED', revision: '5', completedAt: new Date().toISOString() }; orderChanged(); break;
  }
  await route.fulfill({ json: value });
 });
 const page = await context.newPage(); if (user === bob) await page.clock.install(); await page.goto(base + '/orders/' + id);
 await page.locator('.title .status').filter({ hasText: 'Awaiting courier' }).waitFor();
 return { context, page };
}
try {
 const a = await open(alice), b = await open(bob);
 const credit = (page) => page.locator('.credit-balance').first();
 await credit(a.page).filter({ hasText: '100 credits' }).waitFor();
 await credit(b.page).filter({ hasText: '100 credits' }).waitFor();
 balances.set(alice, '90'); notify(alice, ['CHANGE_SCOPE_CREDITS']);
 await credit(a.page).filter({ hasText: '90 credits' }).waitFor({ timeout: 5000 });
 assert.equal(await credit(b.page).textContent(), '100 credits');
 console.log('PASS private credit invalidation updates the shared balance');

 const draft = b.page.getByRole('textbox', { name: 'Counteroffer credits' });
 await draft.fill('35');
 offers = [{ id: offerId, orderId: id, courierId: bob, credits: '25', status: 'OFFER_STATUS_OPEN' }];
 orderChanged();
 await a.page.getByText('25 credits · Open', { exact: true }).waitFor({ timeout: 5000 });
 assert.equal(await draft.inputValue(), '35');
 console.log('PASS offer-only changes arrive without resetting an unsaved proposal');

 const c = await open(alice);
 const browsing = c.page;
 await c.context.addInitScript(() => {
  window.locationReads = 0;
  Object.defineProperty(navigator, 'geolocation', { configurable: true, value: {
   getCurrentPosition(success) { window.locationReads++; success({ coords: { latitude: 1.3, longitude: 103.8 } }); }
  }});
 });
 await browsing.goto(base + '/orders?sort=proximity');
 await browsing.locator('.rows .title').filter({ hasText: 'Live update delivery' }).waitFor();
 await b.page.getByRole('button', { name: 'Accept current reward (20 credits)', exact: true }).click();
 await b.page.getByText('Still confirming your acceptance. Try again to check the result.', { exact: true }).waitFor();
 attemptStatus = 'ASSIGNMENT_ATTEMPT_STATUS_REJECTED'; notify(bob, ['CHANGE_SCOPE_ORDERS']);
 await b.page.getByText('This could not be completed. Choose "Start again" to try again.', { exact: true }).waitFor({ timeout: 5000 });
 assert.equal(accepts, 1, 'checking a pending operation never repeats its mutation');
 console.log('PASS a pending acceptance resolves through a read-only status check');
 await b.page.getByRole('button', { name: 'Start again', exact: true }).click();
 await b.page.getByRole('button', { name: 'Accept current reward (20 credits)', exact: true }).click();
 await a.page.locator('.title .status').filter({ hasText: 'Awaiting pickup' }).waitFor({ timeout: 5000 });
 await browsing.getByText('Nothing matches. Try another destination, or check back soon.').waitFor({ timeout: 5000 });
 await b.page.getByRole('button', { name: 'Confirm pickup', exact: true }).click();
 await a.page.locator('.title .status').filter({ hasText: 'Delivering' }).waitFor({ timeout: 5000 });
 assert(openQueries.length >= 2);
 for (const query of openQueries) {
  assert.equal(query.sort, 'OPEN_ORDER_SORT_PROXIMITY');
  assert.deepEqual(query.origin, { latitude: 1.3, longitude: 103.8 });
 }
 assert.equal(await browsing.evaluate(() => window.locationReads), 1);
 console.log('PASS another participant sees assignment/pickup and marketplace removal; realtime reloads keep the proximity origin');

 order = { ...order, status: 'ORDER_STATUS_DELIVERED', revision: '4', deliveredAt: new Date().toISOString() }; orderChanged();
 await a.page.getByRole('button', { name: 'Confirm receipt', exact: true }).click();
 await b.page.locator('.title .status').filter({ hasText: 'Completed' }).waitFor({ timeout: 5000 });
 assert.equal(await credit(b.page).textContent(), '100 credits');
 balances.set(bob, '120'); notify(bob, ['CHANGE_SCOPE_CREDITS']);
 await credit(b.page).filter({ hasText: '120 credits' }).waitFor({ timeout: 5000 });
 console.log('PASS order completion and subsequent credit settlement update independently');

 balances.set(alice, '80'); closeStreams(alice);
 await credit(a.page).filter({ hasText: '80 credits' }).waitFor({ timeout: 5000 });
 order = { ...order, status: 'ORDER_STATUS_EXPIRED', revision: '6', courierId: undefined }; orderChanged();
 await a.page.locator('.title .status').filter({ hasText: 'Expired' }).waitFor({ timeout: 5000 });
 console.log('PASS reconnect ready refetches missed state and timer-originated updates arrive');

 available = false; closeStreams(bob);
 balances.set(bob, '130');
 await b.page.clock.fastForward(60_001);
 await credit(b.page).filter({ hasText: '130 credits' }).waitFor({ timeout: 5000 });
 console.log('PASS the shared slow fallback repairs state while streaming is unavailable');
 assert.deepEqual(runtimeErrors, []);
} finally {
 await browser.close(); server.closeAllConnections(); await new Promise((resolve) => server.close(resolve));
}
