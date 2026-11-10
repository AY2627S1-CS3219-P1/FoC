// Run against npm run dev; all RPCs are intercepted before they leave the browser.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const base = process.env.MODERATION_TEST_BASE_URL ?? 'http://localhost:5173';
const reviewer = '11111111-1111-4111-8111-111111111111';
const requestId = '22222222-2222-4222-8222-222222222222';
const browser = await chromium.launch();
const errors = [];
const configurationCalls = [];
const context = await browser.newContext();
const page = await context.newPage();
page.on('pageerror', error => errors.push(error.message));
await page.route(/\/[^/]+\.v1\.[^/]+\//, async route => {
	const method = new URL(route.request().url()).pathname.split('/').at(-1);
	if (/Configuration|Draft|AvailableModels|SetupStatus/.test(method)) configurationCalls.push(method);
	const responses = {
		Refresh: () => ({ accessToken: 'browser-fixture', user: { id: reviewer, email: 'admin@example.test', displayName: 'Admin', role: 'USER_ROLE_SUPER_ADMIN' } }),
		GetMyProfile: () => ({ profile: { id: reviewer, email: "admin@example.test", displayName: "Admin", role: "USER_ROLE_SUPER_ADMIN" } }),
		ListUnpublishedReviewsForUser: () => ({ reviews: [{ id: requestId, reviewerId: reviewer, moderationRequestId: requestId, comment: 'Retry progress regression', moderationStatus: 'MODERATION_STATUS_MODERATION_ERROR', appliedRunGeneration: 1, rating: 4 }], pagination: { page: '1', pageSize: '10', totalItems: '1', totalPages: '1' } }),
		GetReviewModerationJobsForReviewer: () => ({ jobs: [{ jobId: requestId, moderationRequestId: requestId, status: 'MODERATION_JOB_STATUS_PROCESSING', runGeneration: 2 }] })
	};
	await route.fulfill({ json: (responses[method] ?? (() => ({})))() });
});
try {
	await page.goto(base + '/admin/config');
	await page.getByRole('heading', { name: 'Config', exact: true }).waitFor();
	assert.equal(await page.getByText('Model configuration', { exact: true }).count(), 0);
	await page.goto(base + '/profile/reviews/unpublished');
	await page.getByText('Being assessed again', { exact: true }).waitFor();
	await page.getByText('Retry progress regression', { exact: true }).waitFor();
	assert.match(await page.locator('.review-detail').innerText(), /unpublished/);
	assert.deepEqual(errors, [], 'no browser runtime errors');
	assert.deepEqual(configurationCalls, [], 'no configuration RPCs');
	console.log('PASS configuration removal and newer-generation retry rendering');
} finally {
	await context.close();
	await browser.close();
}
