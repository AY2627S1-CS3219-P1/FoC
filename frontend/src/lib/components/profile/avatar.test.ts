import { describe, expect, it } from 'vitest';
import { canAttemptPictureRefresh, shouldRefreshPicture } from './avatar.ts';

describe('profile avatar refresh guard', () => {
	it('refreshes only when the signed URL is at or near expiry', () => {
		const now = 1_700_000_000_000;
		expect(shouldRefreshPicture(now + 60_000, now)).toBe(false);
		expect(shouldRefreshPicture(now + 20_000, now)).toBe(true);
		expect(shouldRefreshPicture(now - 1, now)).toBe(true);
	});

	it('does not refresh when there is no expiry to recover', () => {
		expect(shouldRefreshPicture(undefined)).toBe(false);
	});

	it('caps refreshes with a cooldown', () => {
		const now = 1_700_000_000_000;
		expect(canAttemptPictureRefresh(now + 20_000, now - 30_000, now)).toBe(false);
		expect(canAttemptPictureRefresh(now + 20_000, now - 61_000, now)).toBe(true);
		expect(canAttemptPictureRefresh(undefined, 0, now)).toBe(false);
	});
});
