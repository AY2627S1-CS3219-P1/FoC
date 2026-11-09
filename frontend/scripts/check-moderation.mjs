// Run against npm run dev; all RPCs are intercepted before they leave the browser.
import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const base = process.env.MODERATION_TEST_BASE_URL ?? 'http://localhost:5173';
const reviewer = '11111111-1111-4111-8111-111111111111';
const requestId = '22222222-2222-4222-8222-222222222222';
const versionId = '33333333-3333-4333-8333-333333333333';
const parameters = {
	temperature: 0, topP: 0.9, presencePenalty: -0.2, frequencyPenalty: 0.3,
	maxCompletionTokens: '1234', seed: '9007199254740993', stop: ['END,KEEP COMMA', ' other '],
	reasoningEffort: 'high', advancedParameters: { top_k: '40' }
};
const browser = await chromium.launch();
const errors = [];
let saved;
let activated;
let settingsReads = 0;
const context = await browser.newContext();
const page = await context.newPage();
page.on('pageerror', error => errors.push(error.message));
await page.route(/\/[^/]+\.v1\.[^/]+\//, async route => {
	const method = new URL(route.request().url()).pathname.split('/').at(-1);
	const body = JSON.parse(route.request().postData() || '{}');
	const responses = {
		Refresh: () => ({ accessToken: 'browser-fixture', user: { id: reviewer, email: 'admin@example.test', displayName: 'Admin', role: 'USER_ROLE_SUPER_ADMIN' } }),
		GetModerationSetupStatus: () => { settingsReads++; return { hasActiveConfiguration: true, hasProviderCredential: true, policyVersion: 'review-text-v1' }; },
		ListAvailableModels: () => ({ models: [{ modelId: 'fake-model' }] }),
		GetActiveModerationConfiguration: () => ({ configuration: { versionId, modelId: 'fake-model', parameters, outputMode: 'MODERATION_OUTPUT_MODE_JSON_OBJECT', systemMessage: 'Original administrator rubric' } }),
		SaveModerationConfigurationDraft: () => { saved = body; return { draftVersionId: versionId }; },
		ActivateModerationConfiguration: () => { activated = body; return { configuration: { versionId, modelId: 'fake-model', parameters } }; },
		ListUnpublishedReviewsForUser: () => ({ reviews: [{ id: requestId, reviewerId: reviewer, moderationRequestId: requestId, comment: 'Retry progress regression', moderationStatus: 'MODERATION_STATUS_MODERATION_ERROR', appliedRunGeneration: 1, rating: 4 }], pagination: { page: '1', pageSize: '10', totalItems: '1', totalPages: '1' } }),
		GetReviewModerationJobsForReviewer: () => ({ jobs: [{ jobId: requestId, moderationRequestId: requestId, status: 'MODERATION_JOB_STATUS_PROCESSING', runGeneration: 2 }] })
	};
	await route.fulfill({ json: (responses[method] ?? (() => ({})))() });
});
try {
	await page.goto(base + '/admin/config');
	await page.getByLabel('Temperature', { exact: true }).waitFor();
	assert.equal(await page.getByLabel('Temperature', { exact: true }).inputValue(), '0');
	assert.equal(await page.getByLabel('Seed', { exact: true }).inputValue(), parameters.seed);
assert.equal(await page.getByLabel('System message', { exact: true }).inputValue(), 'Original administrator rubric');
await page.getByLabel('System message', { exact: true }).fill('Updated administrator rubric');
	await page.getByText('Resume jobs paused by configuration errors with this version (starts a new generation)', { exact: true }).click();
	assert.equal(await page.getByLabel(/Resume jobs paused/).isChecked(), true);
	await Promise.all([
		page.waitForResponse(response => response.url().endsWith('/SaveModerationConfigurationDraft')),
		page.getByRole('button', { name: 'Save draft', exact: true }).click()
	]);
	assert.equal(saved.systemMessage, 'Updated administrator rubric');
assert.deepEqual(saved.parameters, parameters, 'saving a hydrated configuration preserves all settings');
	await Promise.all([
		page.waitForResponse(response => response.url().endsWith('/ActivateModerationConfiguration')),
		page.getByRole('button', { name: 'Activate draft', exact: true }).click()
	]);
	assert.equal(activated.resumePausedJobs, true);
	assert.equal(activated.expectedActiveVersionId, versionId);
	assert.equal(settingsReads, 1, 'initial configuration loading does not loop');
	await page.goto(base + '/profile/reviews/unpublished');
	await page.getByText('Being assessed again', { exact: true }).waitFor();
	await page.getByText('Retry progress regression', { exact: true }).waitFor();
	assert.match(await page.locator('.review-detail').innerText(), /unpublished/);
	assert.deepEqual(errors, [], 'no browser runtime errors');
	console.log('PASS settings hydration/save, explicit paused-job recovery, and newer-generation retry rendering');
} finally {
	await context.close();
	await browser.close();
}
