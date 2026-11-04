import { describe, expect, it } from 'vitest';
import { payableReward } from './presentation';

// Cases from credit-service/internal/reservation/lateness_test.go, so the UI's
// estimate keeps matching what Credit pays.
describe('payableReward', () => {
	const hour = 3_600_000_000_000n;
	const maxInt64 = 9_223_372_036_854_775_807n;

	it.each([
		['instant', 100n, 0n, 100n],
		['on time', 100n, hour, 100n],
		['just late', 100n, hour + 1n, 99n],
		['half late', 100n, hour + hour / 2n, 50n],
		['rounds down', 7n, hour + hour / 3n, 4n],
		['just before twice', 100n, 2n * hour - 1n, 0n],
		['twice', 100n, 2n * hour, 0n],
		['past twice', 100n, 3n * hour, 0n],
		['huge reward', maxInt64, hour + hour / 2n, maxInt64 / 2n]
	])('%s', (_name, reward, elapsed, want) => {
		expect(payableReward(reward, elapsed, hour)).toBe(want);
	});
});
