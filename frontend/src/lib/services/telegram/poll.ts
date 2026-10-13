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
 * A failed check is retried at the next interval.
 */
export async function pollUntilLinked(
	api: Pick<TelegramLinkApi, 'getStatus'>,
	{ until, intervalMs = 2000, signal, now = Date.now, sleep = defaultSleep }: PollOptions
): Promise<TelegramLinkStatus | null> {
	while (!signal?.aborted && now() < until.getTime()) {
		await sleep(intervalMs, signal);
		if (signal?.aborted) return null;
		try {
			const status = await api.getStatus();
			if (status.linked) return status;
		} catch {
			// Keep waiting; a transient failure shouldn't end linking.
		}
	}
	return null;
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
