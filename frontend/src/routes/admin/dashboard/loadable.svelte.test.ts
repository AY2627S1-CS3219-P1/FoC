import { describe, expect, it, vi } from 'vitest';
import { Loadable } from './loadable.svelte';

function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
	return { promise, resolve, reject };
}

describe('dashboard loading', () => {
	it('clears old figures as soon as a new load starts', async () => {
		const next = deferred<number>();
		const source = new Loadable<number>(vi.fn().mockResolvedValueOnce(7).mockReturnValueOnce(next.promise), 'orders');
		await source.load();
		expect(source.data).toBe(7);
		const loading = source.load();
		expect(source.data).toBeUndefined();
		expect(source.loading).toBe(true);
		next.resolve(90);
		await loading;
		expect(source.data).toBe(90);
		expect(source.loading).toBe(false);
	});

	it.each(['success', 'failure'])('ignores stale %s while a newer request is loading', async (outcome) => {
		const old = deferred<number>();
		const latest = deferred<number>();
		const source = new Loadable<number>(vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise), 'orders');
		const first = source.load();
		const second = source.load();
		if (outcome === 'success') old.resolve(7);
		else old.reject(new Error('stale failure'));
		await first;
		expect(source.data).toBeUndefined();
		expect(source.error).toBe('');
		expect(source.loading).toBe(true);
		latest.resolve(90);
		await second;
		expect(source.data).toBe(90);
	});

	it('keeps independent cards usable when another fails', async () => {
		const orders = new Loadable(async () => { throw new Error('offline'); }, 'orders');
		const credits = new Loadable(async () => 42, 'credits');
		await Promise.all([orders.load(), credits.load()]);
		expect(orders.error).not.toBe('');
		expect(orders.data).toBeUndefined();
		expect(credits.data).toBe(42);
		expect(credits.error).toBe('');
	});
});
