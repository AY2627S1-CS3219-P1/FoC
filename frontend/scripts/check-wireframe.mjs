// Run against `npm run dev` (or set WIREFRAME_BASE_URL). All RPCs are mocked;
// this checks browser state preservation, not backend integration.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const base = process.env.WIREFRAME_BASE_URL ?? 'http://localhost:5173';
const user = { id: 'f0c00001-0000-4000-8000-000000000005', email: 'courier@demo.foc.test', displayName: 'Courier', role: 'USER_ROLE_USER' };
const id = '11111111-1111-4111-8111-111111111111';
const order = {
	id, requesterId: '22222222-2222-4222-8222-222222222222', courierId: user.id,
	status: 'ORDER_STATUS_DELIVERING', itemDescription: 'Proof refresh regression',
	rewardCredits: '20', deliveryDuration: '1800s', revision: '1',
	pickedUpAt: new Date().toISOString(), createdAt: new Date().toISOString(),
	expiresAt: new Date(Date.now() + 3600000).toISOString()
};
const browser = await chromium.launch();
const runtimeErrors = [];

async function open(path, { authenticated = true, overrides = {} } = {}) {
	const context = await browser.newContext({ viewport: { width: 390, height: 900 } });
	const page = await context.newPage();
	page.on('pageerror', error => runtimeErrors.push(error.message));
	await page.route(/tile\.openstreetmap\.org/, route => route.abort());
	await page.route(/localhost:808\d\//, async route => {
		const method = new URL(route.request().url()).pathname.split('/').at(-1);
		if (method === 'Refresh' && !authenticated) {
			return route.fulfill({ status: 401, json: { code: 'unauthenticated', message: 'No session' } });
		}
		const defaults = {
			Refresh: () => ({ accessToken: 'browser-fixture', user }),
			Register: () => ({ accessToken: 'browser-fixture', user }),
			GetOrder: () => ({ order }),
			ListMyOrders: () => ({ orders: [] }),
			GetMyAccount: () => ({ account: { userId: user.id, balance: '100', available: '100', reserved: '0' } }),
			GetDeliveryProof: () => ({}),
			GetTelegramLink: () => ({ linked: false }),
			CreateTelegramLink: () => ({ linkUrl: 'https://t.me/example?start=fixture', expiresAt: new Date(Date.now() + 600000).toISOString() })
		};
		return route.fulfill({ json: (overrides[method] ?? defaults[method] ?? (() => ({})))() });
	});
	await page.goto(base + path);
	return { page, context };
}

try {
	// File objects must survive both ordinary reads and nonterminal revisions.
	let { page, context } = await open('/orders/' + id);
	await page.getByRole('button', { name: 'Update status: delivered' }).click();
	const dialog = page.getByRole('alertdialog');
	await dialog.getByText('0 / 5 selected').waitFor();
	await page.waitForTimeout(600); // Let the opening animation finish.
	await dialog.locator('#delivery-proof-files').setInputFiles({ name: 'proof.png', mimeType: 'image/png', buffer: Buffer.from('fixture') });
	await dialog.getByText('1 / 5 selected').waitFor();
	for (const revision of ['1', '2']) {
		order.revision = revision;
		const response = page.waitForResponse(response => response.url().includes('/GetOrder'));
		await page.evaluate(() => window.dispatchEvent(new Event('online')));
		await response;
		await page.waitForTimeout(500);
		assert(await dialog.getByText('1 / 5 selected').isVisible(), `proof selection survives revision ${revision}`);
	}
	// Switching orders must not carry photos into another request.
	order.id = '33333333-3333-4333-8333-333333333333';
	await page.goto(base + '/orders/' + order.id);
	await page.getByRole('button', { name: 'Update status: delivered' }).click();
	await page.getByRole('alertdialog').getByText('0 / 5 selected').waitFor();
	await context.close();
	order.id = id;
	console.log('PASS selected proof survives ordinary order refresh and revision changes');

	for (const connect of [false, true]) {
		({ page, context } = await open('/register?token=fixture', { authenticated: false }));
		await page.getByRole('textbox', { name: 'Display name' }).fill('New Student');
		await page.getByRole('textbox', { name: 'Phone number' }).fill('+6591234567');
		if (connect) await page.getByText('Connect Telegram (optional)', { exact: true }).click();
		await page.getByRole('button', { name: 'Complete sign-up' }).click();
		if (connect) {
			await page.getByText('Your profile is ready.').waitFor();
			assert.equal(new URL(page.url()).pathname, '/register');
			await page.getByRole('link', { name: 'Open Telegram' }).waitFor();
			await page.getByRole('button', { name: 'Continue to home' }).click();
		}
		await page.waitForURL(base + '/');
		await context.close();
	}
	console.log('PASS registration with and without optional Telegram');

	({ page, context } = await open('/orders/' + id));
	for (const action of ['escape', 'close', 'scrim']) {
		await page.getByRole('button', { name: 'Open menu', exact: true }).click();
		await page.waitForFunction(() => document.activeElement?.getAttribute('aria-label') === 'Close menu');
		if (action === 'escape') await page.keyboard.press('Escape');
		else if (action === 'close') await page.getByRole('button', { name: 'Close menu', exact: true }).click();
		else await page.locator('.scrim').click({ position: { x: 380, y: 500 } });
		await page.waitForFunction(() => document.activeElement?.getAttribute('aria-label') === 'Open menu');
	}
	await context.close();
	console.log('PASS drawer focus on opening and after Escape, close, and scrim');
	assert.deepEqual(runtimeErrors, []);
} finally {
	await browser.close();
}
