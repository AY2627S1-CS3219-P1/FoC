// Browser state checks with mocked RPCs; real SQL ordering is tested in Go.
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
import { chromium } from 'playwright';

const base = process.env.WIREFRAME_BASE_URL ?? 'http://127.0.0.1:5173';
const browser = await chromium.launch();
const errors = [];
const origin = { latitude: 0, longitude: 0 };
const user = { id: '11111111-1111-4111-8111-111111111111', role: 'USER_ROLE_USER', displayName: 'Courier', email: 'courier@example.test' };
const building = { id: '22222222-2222-4222-8222-222222222222', name: 'COM3', center: origin };
const supplier = { id: '33333333-3333-4333-8333-333333333333', name: 'Nearby cafe', building, coordinates: origin, isSupplier: true };
const order = { id: '44444444-4444-4444-8444-444444444444', requesterId: supplier.id, status: 'ORDER_STATUS_AWAITING_COURIER', itemDescription: 'Nearby older meal', pickup: { locationId: supplier.id, name: supplier.name, coordinates: origin }, deliveryBuildingName: building.name, rewardCredits: '10', createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 3600000).toISOString() };
const shots = process.env.PROXIMITY_SCREENSHOT_DIR ?? '/tmp/foc-proximity-screenshots';
await mkdir(shots, { recursive: true });

async function open(path, outcome = 'success', viewport = { width: 390, height: 900 }) {
	const context = await browser.newContext({ viewport });
	await context.addInitScript(({ outcome, origin }) => {
		window.locationCalls = 0;
		window.locationOutcome = outcome;
		Object.defineProperty(navigator, 'geolocation', { configurable: true, value: {
			getCurrentPosition(success, failure, options) {
				window.locationCalls++;
				window.locationOptions = options;
				setTimeout(() => {
					if (window.locationOutcome === 'success') success({ coords: origin });
					else failure({ code: window.locationOutcome === 'timeout' ? 3 : window.locationOutcome === 'unavailable' ? 2 : 1 });
				}, 1500);
			}
		}});
	}, { outcome, origin });
	const page = await context.newPage();
	page.on('pageerror', error => errors.push(error.message));
	const calls = [];
	await page.route(/tile\.openstreetmap\.org/, route => route.abort());
	await page.route(/localhost:808\d\//, async route => {
		const method = new URL(route.request().url()).pathname.split('/').at(-1);
		if (method === 'WatchChanges') return route.fulfill({ status: 501, json: { code: 'unimplemented' } });
		const body = JSON.parse(route.request().postData() || '{}');
		calls.push({ method, body });
		if (method === 'WatchChanges') return route.fulfill({ status: 501, json: { code: 'unimplemented' } });
		const defaults = {
			Refresh: { accessToken: 'fixture', user },
			ListBuildings: { buildings: [building] },
			ListCategories: {},
			ListLocations: { locations: [supplier], page: body.page || 1, pageSize: 20, totalItems: '40', totalPages: 2 },
			GetLocation: { location: supplier },
			ListOpenOrders: { orders: [order], pageInfo: { page: body.page || 1, pageSize: 20, totalItems: '40', totalPages: 2 } },
			ListMyOrders: { orders: [], pageInfo: { page: 1, pageSize: 20 } },
			GetOrder: { order },
			ListOffers: {},
			GetMyAccount: { account: { userId: user.id, balance: '100', available: '100', reserved: '0' } }
		};
		return route.fulfill({ json: defaults[method] ?? {} });
	});
	await page.goto(base + path, { waitUntil: 'domcontentloaded' });
	return { page, context, calls };
}

function assertOrigin(body) {
	assert(body.origin, 'origin message is present even when both coordinates are zero');
	assert.deepEqual({ latitude: body.origin.latitude ?? 0, longitude: body.origin.longitude ?? 0 }, origin);
}

const last = (calls, method) => calls.filter(call => call.method === method).at(-1)?.body;
async function waitCall(page, calls, method, predicate) {
	await page.waitForFunction(() => document.readyState === 'complete');
	for (let i = 0; i < 100; i++) {
		if (predicate(last(calls, method))) return;
		await page.waitForTimeout(50);
	}
	assert.fail(`Expected ${method}, got ${JSON.stringify(last(calls, method))}`);
}

try {
	let { page, context, calls } = await open('/');
	await page.getByText('Finding your location…', { exact: true }).waitFor({timeout: 10000}).catch(async error => { await page.screenshot({path: shots + '/failure.png', fullPage:true}); console.log(await page.locator('body').innerText(), calls, errors); throw error; });
	await waitCall(page, calls, 'ListOpenOrders', body => body?.sort === 'OPEN_ORDER_SORT_PROXIMITY');
	await waitCall(page, calls, 'ListLocations', body => body?.sortField === 'LOCATION_SORT_FIELD_PROXIMITY');
	assertOrigin(last(calls, 'ListOpenOrders'));
	assertOrigin(last(calls, 'ListLocations'));
	assert.equal(await page.evaluate(() => window.locationCalls), 1);
	assert.equal(await page.evaluate(() => window.locationOptions.timeout), 10000);
	const homeReads = calls.filter(call => call.method === 'ListOpenOrders').length;
	await page.evaluate(() => window.dispatchEvent(new Event('focus')));
	for (let i = 0; i < 100 && calls.filter(call => call.method === 'ListOpenOrders').length === homeReads; i++) await page.waitForTimeout(50);
	assert(calls.filter(call => call.method === 'ListOpenOrders').length > homeReads);
	assertOrigin(last(calls, 'ListOpenOrders'));
	await page.getByRole('button', { name: /Nearby older meal/ }).waitFor();
	assert.equal(calls.filter(c => c.method === 'GetLocation').length, 0, 'pickup snapshot avoids per-order lookups');
	await page.getByRole('combobox', { name: 'Request order' }).selectOption('newest');
	await waitCall(page, calls, 'ListOpenOrders', body => body?.sort === 'OPEN_ORDER_SORT_NEWEST');
	assert.match(await page.getByRole('link', { name: 'See all', exact: true }).getAttribute('href'), /sort=newest/);
	await page.getByRole('textbox', { name: 'Search suppliers' }).fill('coffee');
	await page.getByRole('button', { name: 'Search', exact: true }).click();
	await waitCall(page, calls, 'ListLocations', body => body?.search === 'coffee' && body.sortField === 'LOCATION_SORT_FIELD_PROXIMITY');
	await page.getByRole('combobox', { name: 'Supplier order' }).selectOption('name');
	await waitCall(page, calls, 'ListLocations', body => body?.search === 'coffee' && body.sortField === 'LOCATION_SORT_FIELD_NAME');
	assert.equal(await page.evaluate(() => window.locationCalls), 1);
	assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile Home fits');
	await page.screenshot({ path: `${shots}/home-mobile.png`, fullPage: true });
	await context.close();
	console.log('PASS Home shared origin, defaults, search sorting, sort links, snapshot markers, mobile controls');

	for (const outcome of ['denied', 'timeout', 'unavailable']) {
		({ page, context, calls } = await open('/', outcome));
		await page.getByRole('button', { name: 'Retry location', exact: true }).waitFor();
		assert.equal(await page.getByText('Location unavailable.', { exact: false }).count(), 1);
		await waitCall(page, calls, 'ListOpenOrders', body => body?.sort === 'OPEN_ORDER_SORT_NEWEST');
		await waitCall(page, calls, 'ListLocations', body => body?.sortField === 'LOCATION_SORT_FIELD_NAME');
		assert.equal(last(calls, 'ListOpenOrders').origin, undefined);
		await page.evaluate(() => window.locationOutcome = 'success');
		await page.getByRole('button', { name: 'Retry location', exact: true }).click();
		await waitCall(page, calls, 'ListOpenOrders', body => body?.sort === 'OPEN_ORDER_SORT_PROXIMITY');
		assert.equal(await page.evaluate(() => window.locationCalls), 2);
		await context.close();
	}
	console.log('PASS permission denied/unavailable/timeout fallback and retry');

	for (const [path, method, field, nearest, normal] of [
		['/orders', 'ListOpenOrders', 'sort', 'OPEN_ORDER_SORT_PROXIMITY', 'OPEN_ORDER_SORT_NEWEST'],
		['/suppliers', 'ListLocations', 'sortField', 'LOCATION_SORT_FIELD_PROXIMITY', 'LOCATION_SORT_FIELD_NAME']
	]) {
		({ page, context, calls } = await open(path));
		await waitCall(page, calls, method, body => body?.[field] === normal);
		assert.equal(await page.evaluate(() => window.locationCalls), 0, 'direct defaults do not request location');
		await context.close();

		({ page, context, calls } = await open(path + '?sort=proximity&page=2'));
		await waitCall(page, calls, method, body => body?.[field] === nearest && body.page === 2);
		assertOrigin(last(calls, method));
		await page.getByRole('button', { name: 'Previous', exact: true }).click();
		await waitCall(page, calls, method, body => body && (body.page || 1) === 1);
		assertOrigin(last(calls, method));
		if (path === '/suppliers') {
			await page.getByRole('searchbox', { name: 'Search by name', exact: true }).fill('cafe');
			await waitCall(page, calls, method, body => body?.search === 'cafe');
			await page.getByRole('button', { name: 'Show filters', exact: true }).click();
		} else {
			await page.getByRole('combobox', { name: 'Destination', exact: true }).selectOption(building.name);
			await waitCall(page, calls, method, body => body?.deliveryBuildingName === building.name);
			await page.getByRole('button', { name: 'Refresh', exact: true }).click();
		}
		assertOrigin(last(calls, method));
		await page.getByRole('button', { name: 'Next', exact: true }).click();
		await waitCall(page, calls, method, body => body?.page === 2);
		assertOrigin(last(calls, method));
		await page.getByRole('combobox', { name: 'Sort by', exact: true }).selectOption(path === '/orders' ? 'newest' : 'name-asc');
		await waitCall(page, calls, method, body => body?.[field] === normal);
		assert.equal(new URL(page.url()).searchParams.has('page'), false);
		assert.equal(await page.evaluate(() => window.locationCalls), 1);
		assert(!/latitude|longitude|origin/.test(page.url()));
		await page.screenshot({ path: `${shots}/${path.slice(1)}-mobile.png`, fullPage: true });
		await context.close();
	}
	console.log('PASS direct defaults/proximity links, origin retention on pagination/filtering/reload, sort reset');
	assert.deepEqual(errors, []);
} finally {
	await browser.close();
}
