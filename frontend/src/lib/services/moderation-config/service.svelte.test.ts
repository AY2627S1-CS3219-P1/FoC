import { describe, expect, it, vi } from 'vitest';
import { Code, ConnectError } from '@connectrpc/connect';
import { ModerationOutputMode } from '$lib/gen/moderation/v1/config_pb';
import type { ModerationConfigApi, ModerationSetupStatus } from './moderationConfigApi.ts';
import { ModerationConfigService } from './service.svelte.ts';

function deferred<T>() {
	let resolve!: (value: T | PromiseLike<T>) => void;
	const promise = new Promise<T>((resolvePromise) => {
		resolve = resolvePromise;
	});
	return { promise, resolve };
}

function createApi(overrides: Partial<Record<keyof ModerationConfigApi, unknown>> = {}): ModerationConfigApi {
	return {
		getSetupStatus: vi.fn(async () => ({
			hasActiveConfiguration: false,
			hasProviderCredential: true,
			providerFingerprint: '',
			policyVersion: 'review-text-v1'
		})),
		listModels: vi.fn(async () => []),
		getActiveConfiguration: vi.fn(async () => undefined),
		saveDraft: vi.fn(async () => 'draft-1'),
		testDraft: vi.fn(async () => ({
			decision: 'approve',
			reasonCodes: [],
			modelId: 'm',
			outputMode: 'json_object',
			durationMs: 1n,
			errorClass: ''
		})),
		activateDraft: vi.fn(async () => undefined),
		...overrides
	} as ModerationConfigApi;
}

describe('ModerationConfigService', () => {
	it('stores loaded data and reports loading state per operation', async () => {
		const pending = deferred<ModerationSetupStatus>();
		const api = createApi({ getSetupStatus: vi.fn(() => pending.promise) });
		const service = new ModerationConfigService(api);

		const request = service.loadSetup();
		expect(service.isLoading('setup')).toBe(true);
		expect(service.setup).toBeUndefined();

		pending.resolve({ hasActiveConfiguration: true, hasProviderCredential: true, providerFingerprint: 'fp', policyVersion: 'v1' });
		await request;

		expect(service.setup).toMatchObject({ hasActiveConfiguration: true, providerFingerprint: 'fp' });
		expect(service.isLoading('setup')).toBe(false);
		expect(service.error).toBeNull();
	});

	it('captures the error and rethrows while leaving previous data untouched', async () => {
		const api = createApi({ getSetupStatus: vi.fn(async () => ({ providerFingerprint: 'fp' })) });
		const service = new ModerationConfigService(api);
		await service.loadSetup();

		const error = new ConnectError('down', Code.Unavailable);
		vi.mocked(api.getSetupStatus).mockRejectedValueOnce(error);
		await expect(service.loadSetup()).rejects.toBe(error);

		expect(service.error).toBe(error);
		// The previously loaded setup survives the failed refresh.
		expect(service.setup).toMatchObject({ providerFingerprint: 'fp' });
	});

	it('clears the previous error before a retry and stores the new result', async () => {
		const api = createApi({ getSetupStatus: vi.fn(async () => { throw new ConnectError('x', Code.Internal); }) });
		const service = new ModerationConfigService(api);
		await expect(service.loadSetup()).rejects.toBeInstanceOf(ConnectError);
		expect(service.error).not.toBeNull();

		vi.mocked(api.getSetupStatus).mockResolvedValueOnce({ hasActiveConfiguration: false, hasProviderCredential: false, providerFingerprint: '', policyVersion: 'v1' });
		await service.loadSetup();

		expect(service.error).toBeNull();
		expect(service.setup?.hasActiveConfiguration).toBe(false);
	});

	it('surfaces a mutation error without mutating the active configuration', async () => {
		const error = new ConnectError('nope', Code.PermissionDenied);
		const api = createApi({ saveDraft: vi.fn(async () => { throw error; }) });
		const service = new ModerationConfigService(api);

		await expect(
			service.saveDraft('m', { temperature: 0.4 }, ModerationOutputMode.JSON_SCHEMA)
		).rejects.toBe(error);

		expect(service.error).toBe(error);
		expect(service.isLoading('save')).toBe(false);
		expect(service.activeConfiguration).toBeUndefined();
	});

	it('loadAll resolves once every independent read completes', async () => {
		const api = createApi();
		const service = new ModerationConfigService(api);
		await service.loadAll();
		expect(vi.mocked(api.getSetupStatus)).toHaveBeenCalledTimes(1);
		expect(vi.mocked(api.listModels)).toHaveBeenCalledTimes(1);
		expect(vi.mocked(api.getActiveConfiguration)).toHaveBeenCalledTimes(1);
	});

	it('reset clears data, error and loading flags', async () => {
		const api = createApi();
		const service = new ModerationConfigService(api);
		await service.loadAll();

		service.reset();

		expect(service.setup).toBeUndefined();
		expect(service.models).toEqual([]);
		expect(service.activeConfiguration).toBeUndefined();
		expect(service.error).toBeNull();
		expect(service.isBusy).toBe(false);
		expect(service.isLoading('active')).toBe(false);
	});
});
