import { describe, expect, it, vi } from 'vitest';
import { pollUntilLinked } from './poll.ts';

// A fake clock: each sleep advances time instead of waiting.
function clock(start = 0) {
	let now = start;
	return {
		now: () => now,
		sleep: vi.fn(async (ms: number) => {
			now += ms;
		})
	};
}

describe('pollUntilLinked', () => {
	it('returns once the account is linked', async () => {
		const time = clock();
		const getStatus = vi.fn()
			.mockResolvedValueOnce({ linked: false })
			.mockRejectedValueOnce(new Error('network'))
			.mockResolvedValueOnce({ linked: true, username: 'casey' });

		const status = await pollUntilLinked({ getStatus }, { until: new Date(60_000), ...time });

		expect(status).toEqual({ linked: true, username: 'casey' });
		expect(getStatus).toHaveBeenCalledTimes(3);
	});

	it('gives up when the code expires', async () => {
		const time = clock();
		const getStatus = vi.fn(async () => ({ linked: false }));

		expect(await pollUntilLinked({ getStatus }, { until: new Date(10_000), intervalMs: 2000, ...time })).toBeNull();
		expect(getStatus).toHaveBeenCalledTimes(5);
	});

	it('checks once more at expiry', async () => {
		const time = clock();
		const getStatus = vi.fn(async () => ({ linked: time.now() >= 10_000 }));

		const status = await pollUntilLinked({ getStatus }, { until: new Date(10_000), intervalMs: 3000, ...time });

		expect(status).toEqual({ linked: true });
		expect(getStatus).toHaveBeenCalledTimes(4);
	});

	it('stops when aborted', async () => {
		const time = clock();
		const controller = new AbortController();
		const getStatus = vi.fn(async () => {
			controller.abort();
			return { linked: false };
		});

		expect(await pollUntilLinked({ getStatus }, { until: new Date(60_000), signal: controller.signal, ...time })).toBeNull();
		expect(getStatus).toHaveBeenCalledTimes(1);
	});
});
