// Browser interaction checks with mocked Connect RPCs and object storage.
// Real signing, persistence and storage validation belong to backend integration tests.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const base = process.env.WIREFRAME_BASE_URL ?? 'http://localhost:5173';
const user = {
	id: 'f0c00001-0000-4000-8000-000000000005',
	email: 'casey@example.test', displayName: 'Casey', role: 'USER_ROLE_USER'
};
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jY1sAAAAASUVORK5CYII=', 'base64');
const browser = await chromium.launch();
const errors = [];

async function open({ suspended = false, viewport = { width: 1440, height: 1000 } } = {}) {
	const context = await browser.newContext({ viewport });
	const page = await context.newPage();
	page.on('pageerror', error => errors.push(error.message));
	let profile = { ...user, role: suspended ? 'USER_ROLE_SUSPENDED_USER' : user.role, description: '', phoneNumber: '+6591234567' };
	if (suspended) {
		profile = { ...profile, profilePictureId: '00000000-0000-4000-8000-000000000001', profilePictureUrl: 'https://storage.invalid/get/00000000-0000-4000-8000-000000000001', profilePictureExpiresAt: new Date(Date.now() + 300000).toISOString() };
	}
	let grants = 0, uploads = 0, removals = 0;
	const pending = new Set();
	await page.route('https://storage.invalid/**', async route => {
		if (route.request().method() === 'PUT') {
			uploads++;
			assert.equal(route.request().headers()['content-type'], 'image/png');
			assert.equal(route.request().headers()['if-none-match'], '*');
			assert.equal(route.request().headers().cookie, undefined);
			return route.fulfill({ status: 200 });
		}
		return route.fulfill({ contentType: 'image/png', body: png });
	});
	await page.route(/localhost:808\d\//, async route => {
		const method = new URL(route.request().url()).pathname.split('/').at(-1);
		if (method === 'WatchChanges') return route.fulfill({ status: 501, json: { code: 'unimplemented' } });
		const body = JSON.parse(route.request().postData() || '{}');
		let json = {};
		switch (method) {
			case 'Refresh': json = { accessToken: 'browser-fixture', user: profile }; break;
			case 'GetMyProfile': json = { profile }; break;
			case 'GetMyAccount': json = { account: { userId: user.id, balance: '100', available: '100', reserved: '0' } }; break;
			case 'GetTelegramLink': json = { linked: false }; break;
			case 'ListMyOrders': json = { orders: [], pageInfo: { page: 1, pageSize: 20, totalPages: 0 } }; break;
			case 'CreateProfilePictureUpload': {
				assert.equal(suspended, false);
				assert.equal(body.contentType, 'image/png');
				assert.equal(Number(body.sizeBytes), png.length);
				const pictureId = `00000000-0000-4000-8000-${String(++grants).padStart(12, '0')}`;
				pending.add(pictureId);
				json = { pictureId, uploadUrl: `https://storage.invalid/put/${pictureId}`, uploadHeaders: { 'Content-Type': 'image/png', 'If-None-Match': '*' }, expiresAt: new Date(Date.now() + 600000).toISOString() };
				break;
			}
			case 'UpdateMyProfilePicture':
				assert(pending.has(body.pictureId));
				assert.equal(uploads, grants, 'the bytes reach storage before apply');
				profile = { ...profile, profilePictureId: body.pictureId, profilePictureUrl: `https://storage.invalid/get/${body.pictureId}`, profilePictureExpiresAt: new Date(Date.now() + 300000).toISOString() };
				json = { profile }; break;
			case 'RemoveMyProfilePicture':
				assert.equal(suspended, false);
				removals++;
				profile = { ...profile, profilePictureId: '', profilePictureUrl: '', profilePictureExpiresAt: undefined };
				json = { profile }; break;
		}
		return route.fulfill({ json });
	});
	await page.goto(base + '/profile');
	await page.getByRole('heading', { name: 'Casey', exact: true }).waitFor();
	return { page, context, counts: () => ({ grants, uploads, removals }) };
}

async function selectPicture(page) {
	const chooser = page.waitForEvent('filechooser');
	const add = page.getByRole('button', { name: 'Add profile picture', exact: true });
	if (await add.count()) await add.click();
	else await page.getByText('Update profile picture', { exact: true }).click();
	await (await chooser).setFiles({ name: 'avatar.png', mimeType: 'image/png', buffer: png });
	await page.getByRole('button', { name: 'Update profile picture', exact: true }).waitFor();
}

async function assertAvatars(page, pictureNumber) {
	const suffix = String(pictureNumber).padStart(12, '0');
	await page.waitForFunction(suffix => {
		const pictures = [...document.querySelectorAll('img.avatar')];
		return pictures.length === 2 && pictures.every(image => image.src.endsWith(suffix) && image.complete && image.naturalWidth > 0);
	}, suffix);
}

try {
	let { page, context, counts } = await open();
	await page.getByRole('button', { name: 'Add profile picture', exact: true }).waitFor();
	await selectPicture(page);
	await assertAvatars(page, 1);
	await page.getByRole('button', { name: 'Update profile picture', exact: true }).click();
	await page.getByText('Update profile picture', { exact: true }).waitFor();
	await selectPicture(page);
	await assertAvatars(page, 2);
	await page.getByRole('button', { name: 'Update profile picture', exact: true }).click();
	await page.getByText('Remove profile picture', { exact: true }).click();
	const confirmation = page.getByRole('alertdialog', { name: 'Remove profile picture?' });
	await confirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
	assert.equal(counts().removals, 0);
	await assertAvatars(page, 2);
	await page.getByRole('button', { name: 'Update profile picture', exact: true }).click();
	await page.getByText('Remove profile picture', { exact: true }).click();
	await confirmation.getByRole('button', { name: 'Remove', exact: true }).click();
	await page.getByRole('button', { name: 'Add profile picture', exact: true }).waitFor();
	await page.waitForFunction(() => document.querySelectorAll('img.avatar').length === 0);
	assert.deepEqual(counts(), { grants: 2, uploads: 2, removals: 1 });
	await context.close();
	console.log('PASS upload, replace, cancel removal and confirmed removal synchronize both avatars');

	({ page, context, counts } = await open({ viewport: { width: 390, height: 844 } }));
	await selectPicture(page);
	await page.getByRole('button', { name: 'Update profile picture', exact: true }).click();
	const menu = page.locator('.menu-pop');
	await menu.waitFor();
	const box = await menu.boundingBox();
	assert(box && box.x >= 0 && box.x + box.width <= 390 && box.y >= 0 && box.y + box.height <= 844, 'mobile picture menu stays in viewport');
	await page.keyboard.press('Escape');
	await menu.waitFor({ state: 'hidden' });
	await context.close();
	console.log('PASS mobile picture menu fits viewport and closes with Escape');

	({ page, context, counts } = await open({ suspended: true }));
	assert(await page.getByRole('button', { name: 'Profile picture', exact: true }).isDisabled());
	await assertAvatars(page, 1);
	assert.equal(await page.getByRole('button', { name: 'Add profile picture', exact: true }).count(), 0);
	assert.deepEqual(counts(), { grants: 0, uploads: 0, removals: 0 });
	await context.close();
	console.log('PASS suspended account keeps a read-only avatar');
	assert.deepEqual(errors, []);
	console.log('PASS no browser runtime errors');
} finally {
	await browser.close();
}
