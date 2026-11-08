// Run against `npm run dev` (or set WIREFRAME_BASE_URL). All RPCs are mocked;
// this checks browser state preservation, not backend integration.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
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
const screenshots = process.env.WIREFRAME_SCREENSHOT_DIR;
if (screenshots) await mkdir(screenshots, { recursive: true });

async function open(path, { authenticated = true, overrides = {}, viewport = { width: 390, height: 900 } } = {}) {
	const context = await browser.newContext({ viewport });
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
			ListMyOrders: () => ({ orders: [], pageInfo: { page: 1, pageSize: 20, totalPages: 0 } }),
			ListOpenOrders: () => ({ orders: [], pageInfo: { page: 1, pageSize: 20, totalPages: 0 } }),
			GetMyAccount: () => ({ account: { userId: user.id, balance: '100', available: '100', reserved: '0' } }),
			GetDeliveryProof: () => ({}),
			GetTelegramLink: () => ({ linked: false }),
			CreateTelegramLink: () => ({ linkUrl: 'https://t.me/example?start=fixture', expiresAt: new Date(Date.now() + 600000).toISOString() })
		};
		const body = JSON.parse(route.request().postData() || '{}');
		const json = await (overrides[method] ?? defaults[method] ?? (() => ({})))(body);
		return route.fulfill({ json });
	});
	await page.goto(base + path);
	return { page, context };
}

try {
	// Page controls must paint behind the modal drawer, including Leaflet controls.
	let { page, context } = await open('/', { viewport: { width: 512, height: 685 } });
	await page.getByRole('button', { name: 'Open menu', exact: true }).click();
	await page.getByText('Nothing in progress.', { exact: true }).waitFor();
	await page.waitForTimeout(400);
	const drawerClip = { x: 0, y: 0, width: 310, height: 650 };
	const controlsVisible = await page.screenshot({ clip: drawerClip, animations: 'disabled' });
	if (screenshots) await page.screenshot({ path: `${screenshots}/drawer-fixed.png`, animations: 'disabled' });
	await page.locator('.map-toggle, .leaflet-control-container').evaluateAll(elements => {
		for (const element of elements) element.style.visibility = 'hidden';
	});
	const controlsHidden = await page.screenshot({ clip: drawerClip, animations: 'disabled' });
	assert(controlsVisible.equals(controlsHidden), 'map controls do not paint over the drawer');
	await context.close();
	console.log('PASS drawer paints above map switches and Leaflet controls');

	({ page, context } = await open('/suppliers', { overrides: {
		ListLocations: body => ({
			locations: Array.from({ length: 20 }, (_, i) => ({ id: `location-${body.page}-${i}`, name: `Page ${body.page || 1} supplier ${i}` })),
			page: body.page || 1, pageSize: 20, totalItems: '40', totalPages: 2
		})
	} }));
	for (const [button, targetPage] of [['Next', 2], ['Previous', 1]]) {
		await page.getByRole('button', { name: button, exact: true }).scrollIntoViewIfNeeded();
		assert(await page.evaluate(() => window.scrollY > 200), 'pagination starts from the bottom of a long list');
		await page.getByRole('button', { name: button, exact: true }).click();
		await page.getByText(`Page ${targetPage} supplier 0`, { exact: true }).waitFor();
		await page.waitForFunction(() => window.scrollY < 10, null, { timeout: 5000 });
	}
	await page.getByRole('button', { name: 'Show filters', exact: true }).click();
	await page.getByRole('combobox', { name: 'Building', exact: true }).waitFor();
	assert.equal(await page.getByRole('button', { name: 'Hide filters', exact: true }).getAttribute('aria-expanded'), 'true');
	if (screenshots) await page.screenshot({ path: `${screenshots}/supplier-filters.png`, animations: 'disabled' });
	const supplierSearch = page.getByRole('searchbox', { name: 'Search by name', exact: true });
	await supplierSearch.fill('coffee');
	const filterScroll = await page.evaluate(() => window.scrollY);
	await page.waitForURL(url => url.searchParams.get('search') === 'coffee');
	assert(await supplierSearch.evaluate(element => element === document.activeElement), 'filter typing keeps focus');
	assert(Math.abs(await page.evaluate(() => window.scrollY) - filterScroll) < 2, 'filter changes keep scroll');
	await context.close();
	console.log('PASS supplier pagination returns to top; filter toggle expands its controls');

	// History can name pickups outside the first supplier page, including ordinary locations.
	const supplierPage = Array.from({ length: 100 }, (_, i) => ({ id: `supplier-${i}`, name: `Supplier ${i}` }));
	const recent = [
		{ id: 'supplier-101', name: 'Recent supplier beyond page one' },
		{ id: 'ordinary', name: 'Recent ordinary pickup', isSupplier: false },
		{ id: 'archived', name: 'Archived recent pickup', archivedAt: new Date().toISOString() }
	];
	const reads = [];
	({ page, context } = await open('/', { overrides: {
		ListLocations: async () => {
			await new Promise(resolve => setTimeout(resolve, 150));
			return { locations: supplierPage, page: 1, pageSize: 100, totalPages: 2 };
		},
		ListMyOrders: body => ({ orders: body.role ? [...recent, { id: 'unavailable' }].map(location => ({ ...order, pickup: { locationId: location.id } })) : [], pageInfo: { page: 1, pageSize: 20, totalPages: 1 } }),
		GetLocation: body => {
			reads.push(body.id);
			return { location: recent.find(location => location.id === body.id) };
		}
	} }));
	await page.getByRole('heading', { name: 'Recent', exact: true }).waitFor({ timeout: 5000 });
	for (const location of recent) {
		await page.getByRole('button', { name: location.name, exact: true }).waitFor();
	}
	assert(reads.includes('supplier-101') && reads.includes('ordinary') && reads.includes('archived'));
	const archived = page.locator('li').filter({ has: page.getByRole('button', { name: 'Archived recent pickup' }) });
	await archived.getByText('Archived', { exact: true }).waitFor();
	assert.equal(await archived.getByRole('link', { name: 'Request', exact: true }).count(), 0);
	assert.equal(await page.getByRole('button', { name: 'unavailable', exact: true }).count(), 0);
	await context.close();
	console.log('PASS recent pickups outside supplier page one are loaded; unavailable locations are omitted');

	({ page, context } = await open('/orders/mine', { authenticated: false }));
	const signIn = page.getByRole('link', { name: 'Sign in', exact: true });
	await signIn.waitFor({ timeout: 5000 });
	await signIn.click();
	await page.waitForURL(base + '/login');
	await context.close();
	console.log('PASS signed-out order history provides a working sign-in link');

	// File objects must survive both ordinary reads and nonterminal revisions.
	({ page, context } = await open('/orders/' + id));
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
