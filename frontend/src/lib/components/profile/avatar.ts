const refreshWindowMs = 30_000;

/**
 * A signed picture URL can fail because it expired or because the object is
 * genuinely unavailable. Only refresh when the URL is at or near expiry;
 * refreshing a fresh-but-broken URL would mint another fresh URL and loop
 * forever.
 */
export function shouldRefreshPicture(expiresAt: number | undefined, now = Date.now()): boolean {
	return expiresAt !== undefined && now >= expiresAt - refreshWindowMs;
}

const refreshCooldownMs = 60_000;

/** Caps refreshes even when the client clock is ahead of the server expiry. */
export function canAttemptPictureRefresh(
	expiresAt: number | undefined,
	lastAttemptAt: number,
	now = Date.now()
): boolean {
	return now - lastAttemptAt >= refreshCooldownMs && shouldRefreshPicture(expiresAt, now);
}
