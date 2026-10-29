import { describe, expect, it, vi } from 'vitest';
import type { AllowedDomainApi } from './allowedDomainApi.ts';
import { AllowedDomainService } from './service.svelte.ts';

function createApi(): AllowedDomainApi {
	return {
		listAllowedDomains: vi.fn(async () => []),
		allowDomain: vi.fn(async () => undefined),
		removeAllowedDomain: vi.fn(async () => undefined)
	};
}

function deferred<T>() {
	let resolve!: (value: T | PromiseLike<T>) => void;
	const promise = new Promise<T>((resolvePromise) => {
		resolve = resolvePromise;
	});
	return { promise, resolve };
}

describe('AllowedDomainService', () => {
	it('stores domains returned by the API and exposes loading state', async () => {
		const api = createApi();
		const pending = deferred<Awaited<ReturnType<AllowedDomainApi['listAllowedDomains']>>>();
		vi.mocked(api.listAllowedDomains).mockReturnValueOnce(pending.promise);
		const service = new AllowedDomainService(api);

		const request = service.listAllowedDomains();
		expect(service.isLoading).toBe(true);
		expect(service.hasLoaded).toBe(false);

		pending.resolve([{ id: 'domain-1', domain: 'example.com' }]);
		await request;

		expect(service.domains).toEqual([{ id: 'domain-1', domain: 'example.com' }]);
		expect(service.hasLoaded).toBe(true);
		expect(service.isLoading).toBe(false);
		expect(service.loadError).toBeNull();
	});

	it('retains loaded domains when a refresh fails', async () => {
		const api = createApi();
		vi.mocked(api.listAllowedDomains).mockResolvedValueOnce([
			{ id: 'domain-1', domain: 'example.com' }
		]);
		const service = new AllowedDomainService(api);
		await service.listAllowedDomains();
		const error = new Error('refresh failed');
		vi.mocked(api.listAllowedDomains).mockRejectedValueOnce(error);

		await expect(service.listAllowedDomains()).rejects.toBe(error);

		expect(service.domains).toEqual([{ id: 'domain-1', domain: 'example.com' }]);
		expect(service.hasLoaded).toBe(true);
		expect(service.isLoading).toBe(false);
		expect(service.loadError).toBe(error);
	});

	it('removes a domain from service state only after the API succeeds', async () => {
		const api = createApi();
		vi.mocked(api.listAllowedDomains).mockResolvedValueOnce([
			{ id: 'domain-1', domain: 'example.com' },
			{ id: 'domain-2', domain: 'example.org' }
		]);
		const service = new AllowedDomainService(api);
		await service.listAllowedDomains();

		await service.removeAllowedDomain('domain-1');

		expect(api.removeAllowedDomain).toHaveBeenCalledWith('domain-1');
		expect(service.domains).toEqual([{ id: 'domain-2', domain: 'example.org' }]);
	});
});
