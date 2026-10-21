import type { TelegramLinkApi, TelegramLinkStatus } from './telegramLinkApi.ts';

export interface PollOptions {
	/** Stop waiting at this time; the link code expires then. */
	until: Date;
	intervalMs?: number;
	signal?: AbortSignal;
	now?: () => number;
	sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
}

/**
 * Checks the link status until the account is linked, the code expires or the
 * signal aborts. Returns the linked status, or null if it stopped first.
 * A failed check is retried at the next interval. The status is checked once
 * more at expiry, so a link completed just before then isn't reported expired.
 */
export async function pollUntilLinked(
	api: Pick<TelegramLinkApi, 'getStatus'>,
	{ until, intervalMs = 2000, signal, now = Date.now, sleep = defaultSleep }: PollOptions
): Promise<TelegramLinkStatus | null> {
	while (!signal?.aborted && now() < until.getTime()) {
		await sleep(Math.max(0, Math.min(intervalMs, until.getTime() - now())), signal);
		if (signal?.aborted) return null;
		const status = await check(api);
		if (status) return status;
	}
	return null;
}

/** Returns the status if linked; a failed check counts as not linked yet. */
async function check(api: Pick<TelegramLinkApi, 'getStatus'>): Promise<TelegramLinkStatus | null> {
	try {
		const status = await api.getStatus();
		return status.linked ? status : null;
	} catch {
		// Keep waiting; a transient failure shouldn't end linking.
		return null;
	}
}

function defaultSleep(ms: number, signal?: AbortSignal): Promise<void> {
	return new Promise((resolve) => {
		const timer = setTimeout(resolve, ms);
		signal?.addEventListener('abort', () => {
			clearTimeout(timer);
			resolve();
		}, { once: true });
	});
}
