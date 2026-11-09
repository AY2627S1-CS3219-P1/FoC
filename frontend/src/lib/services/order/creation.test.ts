import { describe, expect, it } from 'vitest';
import { creationSchema, putImage, recoverSubmission, saveSubmission, clearSubmittedOrders, submissionStoragePrefix, validateImage, type SubmittedCreation } from './creation.ts';

const callerId = '11111111-1111-4111-8111-111111111111';
const key = '22222222-2222-4222-8222-222222222222';
const pickup = '33333333-3333-4333-8333-333333333333';
const delivery = '44444444-4444-4444-8444-444444444444';
const image = '55555555-5555-4555-8555-555555555555';
const submittedAt = Date.parse('2026-10-18T10:00:00Z');

function storage(): Storage {
	const values = new Map<string, string>();
	return { get length() { return values.size; }, key: (i) => [...values.keys()][i] ?? null,
		getItem: (name) => values.get(name) ?? null, setItem: (name, value) => { values.set(name, value); },
		removeItem: (name) => { values.delete(name); }, clear: () => values.clear() };
}

function submission(): SubmittedCreation {
	return { version: 1, callerId, key, submittedAt, input: {
		pickupLocationId: pickup, deliveryLocationId: delivery, instructions: 'Room 02-15',
		itemDescription: 'Two meals', rewardCredits: '20', deliveryMinutes: 30,
		expiresAt: '2026-10-18T11:00:00.000Z', itemImageIds: [image]
	} };
}

// Refresh retries the exact submitted fields, image IDs and identity.
describe('submitted creation recovery', () => {
	it('recovers the exact submission after refresh, including a now-past expiry', () => {
		const tab = storage();
		const original = submission();
		saveSubmission(tab, original);
		expect(recoverSubmission(tab, callerId, submittedAt + 2 * 3600000)).toEqual({ state: 'ready', submission: original });
		expect(tab.getItem(submissionStoragePrefix + callerId)).not.toContain('uploadUrl');
	});

	it('does not expose one callers submission to another caller', () => {
		const tab = storage();
		saveSubmission(tab, submission());
		expect(recoverSubmission(tab, pickup, submittedAt)).toEqual({ state: 'none' });
	});

	it('blocks corrupt and expired identities instead of silently starting another Order', () => {
		const tab = storage();
		saveSubmission(tab, submission());
		expect(recoverSubmission(tab, callerId, submittedAt + 24 * 3600000).state).toBe('blocked');
		tab.setItem(submissionStoragePrefix + callerId, '{bad');
		expect(recoverSubmission(tab, callerId, submittedAt).state).toBe('blocked');
	});

	it('clears submission data on logout without deleting unrelated tab data', () => {
		const tab = storage();
		saveSubmission(tab, submission());
		tab.setItem('unrelated', 'keep');
		clearSubmittedOrders(tab);
		expect(recoverSubmission(tab, callerId, submittedAt)).toEqual({ state: 'none' });
		expect(tab.getItem('unrelated')).toBe('keep');
	});
});

// Proto CreateOrder/CreateItemImageUpload and NFR-18 define independent boundaries.
describe('creation contract validation', () => {
	it('accepts distinct locations and rejects the same pickup/delivery', () => {
		expect(creationSchema.safeParse(submission().input).success).toBe(true);
		expect(creationSchema.safeParse({ ...submission().input, deliveryLocationId: pickup }).success).toBe(false);
	});

	it('enforces 5-minute/24-hour delivery limits and whole positive int64 credits', () => {
		for (const deliveryMinutes of [5, 1440]) expect(creationSchema.safeParse({ ...submission().input, deliveryMinutes }).success).toBe(true);
		for (const deliveryMinutes of [4, 1441]) expect(creationSchema.safeParse({ ...submission().input, deliveryMinutes }).success).toBe(false);
		for (const rewardCredits of ['0', '-1', '1.5', '9223372036854775808']) expect(creationSchema.safeParse({ ...submission().input, rewardCredits }).success).toBe(false);
	});

	it('counts description Unicode codepoints and enforces exact decimal MB size', () => {
		expect(creationSchema.safeParse({ ...submission().input, itemDescription: '🍱'.repeat(1000) }).success).toBe(true);
		expect(creationSchema.safeParse({ ...submission().input, itemDescription: '🍱'.repeat(1001) }).success).toBe(false);
		expect(validateImage({ type: 'image/jpeg', size: 10000000 })).toBeUndefined();
		expect(validateImage({ type: 'image/jpeg', size: 10000001 })).toContain('10,000,000');
		expect(validateImage({ type: 'text/html', size: 10 })).toContain('JPEG');
	});
});

// Lost upload responses stay unknown; only explicit CreateOrder verifies.
describe('signed image upload', () => {
	const file = new File(['image'], 'food.jpg', { type: 'image/jpeg' });
	const grant = { imageId: image, uploadUrl: 'https://storage.example/object?signed=yes', uploadHeaders: { 'Content-Type': 'image/jpeg', 'If-None-Match': '*' } };

	it('sends object bytes and signed headers without cookies or bearer credentials', async () => {
		let seen: RequestInit | undefined;
		const send = (async (_url: unknown, options: RequestInit) => { seen = options; return new Response('', { status: 200 }); }) as typeof fetch;
		expect(await putImage(file, grant, send)).toBe('uploaded');
		expect(seen).toMatchObject({ method: 'PUT', body: file, credentials: 'omit', redirect: 'error', headers: grant.uploadHeaders });
	});

	it('keeps a lost response or write-once repeat uncertain rather than claiming success', async () => {
		expect(await putImage(file, grant, (async () => { throw new TypeError('connection lost'); }) as typeof fetch)).toBe('unknown');
		expect(await putImage(file, grant, (async () => new Response('', { status: 412 })) as typeof fetch)).toBe('unknown');
	});
});
