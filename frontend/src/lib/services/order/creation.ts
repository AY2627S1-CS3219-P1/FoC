import { z } from 'zod';
import type { CreateOrderInput, ItemImageGrant } from './orderApi.ts';

export const maxImageBytes = 10000000;
export const imageTypes = ['image/jpeg', 'image/png', 'image/webp', 'image/heic', 'image/heif'];
const textLength = (value: string) => Array.from(value).length;
const uuid = z.string().uuid('Select a location from search results.');

export const creationSchema = z.object({
	pickupLocationId: uuid,
	deliveryLocationId: uuid,
	instructions: z.string().trim().refine((s) => textLength(s) <= 500, 'Use 500 characters or fewer.'),
	itemDescription: z.string().trim().refine((s) => textLength(s) >= 1 && textLength(s) <= 1000, 'Describe the item in 1–1000 characters.'),
	rewardCredits: z.string().regex(/^[0-9]+$/, 'Enter a positive whole number of credits.').refine((s) => {
		try { return BigInt(s) > 0n && BigInt(s) <= 9223372036854775807n; } catch { return false; }
	}, 'Enter credits between 1 and 9223372036854775807.'),
	deliveryMinutes: z.number().int().min(5, 'Allow at least 5 minutes after pickup.').max(1440, 'Allow no more than 24 hours after pickup.'),
	expiresAt: z.string().refine((s) => Number.isFinite(new Date(s).getTime()), 'Enter a valid expiry date and time.')
}).refine((data) => data.pickupLocationId !== data.deliveryLocationId, {
	path: ['deliveryLocationId'], message: 'Choose different pickup and delivery locations.'
});

export type CreationForm = z.infer<typeof creationSchema>;

export interface SubmittedCreation {
	version: 1;
	callerId: string;
	key: string;
	submittedAt: number;
	input: CreateOrderInput;
	orderId?: string;
}

export const submissionStoragePrefix = 'foc.order.submission.';
const retryLifetime = 24 * 60 * 60 * 1000;

const savedSchema = z.object({
	version: z.literal(1), callerId: z.string().uuid(), key: z.string().uuid(),
	submittedAt: z.number().int().nonnegative(),
	input: creationSchema.safeExtend({ itemImageIds: z.array(z.string().uuid()).max(5) }),
	orderId: z.string().uuid().optional()
});

// Logout keeps only this identity, never the location/instruction/image payload.
const identitySchema = savedSchema.omit({ input: true });

export type RecoveredSubmission =
	{ state: 'none' } | { state: 'ready'; submission: SubmittedCreation } | { state: 'blocked'; message: string };

/** Never silently discard unreadable or expired retry identities and start a new Order. */
export function recoverSubmission(storage: Storage, callerId: string, now = Date.now()): RecoveredSubmission {
	try {
		const raw = storage.getItem(submissionStoragePrefix + callerId);
		if (!raw) return { state: 'none' };
		const value = JSON.parse(raw);
		if (value?.redacted === true) {
			return { state: 'blocked', message: 'A previous submission was unresolved when you signed out. Check My Orders before starting another order.' };
		}
		const result = savedSchema.safeParse(value);
		if (!result.success || result.data.callerId !== callerId) {
			return { state: 'blocked', message: 'Saved submission cannot be read safely. Check My Orders before starting another order.' };
		}
		if (now - result.data.submittedAt >= retryLifetime || result.data.submittedAt > now) {
			return { state: 'blocked', message: 'The saved submission is outside its safe retry period. Check My Orders; do not automatically resubmit.' };
		}
		return { state: 'ready', submission: result.data };
	} catch {
		return { state: 'blocked', message: 'Submission recovery is unavailable. Enable tab storage before submitting an order.' };
	}
}

export function saveSubmission(storage: Storage, value: SubmittedCreation): void {
	storage.setItem(submissionStoragePrefix + value.callerId, JSON.stringify(value));
}

export function removeSubmission(storage: Storage, callerId: string): void {
	storage.removeItem(submissionStoragePrefix + callerId);
}

/** Clear sensitive values on logout while retaining an unresolved-submission guard. */
export function clearSubmittedOrders(storage: Storage): void {
	for (let i = storage.length - 1; i >= 0; i--) {
		const key = storage.key(i);
		if (!key?.startsWith(submissionStoragePrefix)) continue;
		let identity = {};
		try {
			const result = identitySchema.safeParse(JSON.parse(storage.getItem(key) ?? 'null'));
			if (result.success) identity = result.data;
		} catch { /* An unreadable identity must still block a fresh submission. */ }
		storage.setItem(key, JSON.stringify({ ...identity, redacted: true }));
	}
}

export function validateImage(file: Pick<File, 'size' | 'type'>): string | undefined {
	if (!imageTypes.includes(file.type)) return 'Choose JPEG, PNG, WebP, HEIC or HEIF.';
	if (file.size < 1 || file.size > maxImageBytes) return 'Each image must contain 1–10,000,000 bytes (10 MB).';
}

/** Upload object bytes without forwarding the application's bearer token or cookies. */
export async function putImage(file: File, grant: ItemImageGrant, send: typeof fetch = fetch): Promise<'uploaded' | 'unknown'> {
	try {
		const response = await send(grant.uploadUrl, {
			method: 'PUT', body: file, headers: grant.uploadHeaders, credentials: 'omit', redirect: 'error'
		});
		if (response.ok) return 'uploaded';
		// A lost successful response followed by a repeat can produce 412.
		// CreateOrder, not this status code, verifies the stored object.
		if (response.status === 412) return 'unknown';
		throw new Error('Image upload was rejected (' + response.status + '). Retry this image or remove it.');
	} catch (error) {
		if (error instanceof TypeError) return 'unknown';
		throw error;
	}
}
