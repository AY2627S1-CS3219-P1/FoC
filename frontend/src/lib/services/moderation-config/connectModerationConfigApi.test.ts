import { describe, expect, it, vi } from 'vitest';
import { Code, ConnectError, type Client } from '@connectrpc/connect';
import {
	ModerationAdminService,
	ModerationOutputMode,
	type ModerationConfiguration,
	type SaveModerationConfigurationDraftRequest
} from '$lib/gen/moderation/v1/config_pb';
import { ConnectModerationConfigApi } from './connectModerationConfigApi.ts';

function makeClient(overrides: Record<string, unknown>): Client<typeof ModerationAdminService> {
	return overrides as unknown as Client<typeof ModerationAdminService>;
}

describe('ConnectModerationConfigApi', () => {
	it('maps the setup status, model list and active configuration', async () => {
		const client = makeClient({
			getModerationSetupStatus: vi.fn(async () => ({
				hasActiveConfiguration: true,
				hasProviderCredential: true,
				providerFingerprint: 'fp-1',
				policyVersion: 'review-text-v1'
			})),
			listAvailableModels: vi.fn(async () => ({
				models: [{ $typeName: 'moderation.v1.ModerationModel', modelId: 'ornith-1.5', ownedBy: 'provider' }]
			})),
			getActiveModerationConfiguration: vi.fn(async () => ({
				configuration: {
					$typeName: 'moderation.v1.ModerationConfiguration',
					versionId: 'v-1',
					modelId: 'ornith-1.5',
					outputMode: ModerationOutputMode.JSON_SCHEMA,
					policyVersion: 'review-text-v1'
				}
			}))
		});
		const api = new ConnectModerationConfigApi(client);

		expect(await api.getSetupStatus()).toEqual({
			hasActiveConfiguration: true,
			hasProviderCredential: true,
			providerFingerprint: 'fp-1',
			policyVersion: 'review-text-v1'
		});
		expect((await api.listModels())[0]?.modelId).toBe('ornith-1.5');
		const active = await api.getActiveConfiguration();
		expect(active?.versionId).toBe('v-1');
		expect(active?.outputMode).toBe(ModerationOutputMode.JSON_SCHEMA);
	});

	it('returns no active configuration when the server omits it', async () => {
		const api = new ConnectModerationConfigApi(makeClient({ getActiveModerationConfiguration: vi.fn(async () => ({ configuration: undefined })) }));
		expect(await api.getActiveConfiguration()).toBeUndefined();
	});

	it('sends the built parameters and returns the draft version id', async () => {
		const save = vi.fn(async (_request: SaveModerationConfigurationDraftRequest) => ({ $typeName: 'moderation.v1.SaveModerationConfigurationDraftResponse', draftVersionId: 'draft-1' }));
		const api = new ConnectModerationConfigApi(makeClient({ saveModerationConfigurationDraft: save }));

		const draftVersionId = await api.saveDraft('ornith-1.5', { temperature: 0.3, maxTokens: 512 }, ModerationOutputMode.JSON_OBJECT);

		expect(draftVersionId).toBe('draft-1');
		const request = save.mock.calls[0][0];
		expect(request.modelId).toBe('ornith-1.5');
		expect(request.outputMode).toBe(ModerationOutputMode.JSON_OBJECT);
		// The parameters are built by the shared builder: defaults fill the unset
		// doubles and the token budget is converted to an int64.
		expect(request.parameters).toMatchObject({
			temperature: 0.3,
			topP: 0,
			presencePenalty: 0,
			frequencyPenalty: 0,
			stop: [],
			maxTokens: 512n,
			advancedParameters: {}
		});
	});

	it('throws before sending when a draft parameter is rejected by the allowlist', async () => {
		const save = vi.fn(async () => ({ draftVersionId: 'should-not-be-called' }));
		const api = new ConnectModerationConfigApi(makeClient({ saveModerationConfigurationDraft: save }));

		await expect(api.saveDraft('m', { advancedParameters: { model: 'hijack' } }, ModerationOutputMode.JSON_SCHEMA)).rejects.toThrow();
		expect(save).not.toHaveBeenCalled();
	});

	it('forwards the draft test and activation payloads', async () => {
		const test = vi.fn(async () => ({
			$typeName: 'moderation.v1.TestModerationConfigurationDraftResponse',
			decision: 'reject',
			reasonCodes: ['SPAM'],
			modelId: 'ornith-1.5',
			outputMode: 'json_object',
			durationMs: 12n,
			errorClass: ''
		}));
		const activate = vi.fn(async () => ({ configuration: { $typeName: 'moderation.v1.ModerationConfiguration', versionId: 'v-2' } }));
		const api = new ConnectModerationConfigApi(makeClient({ testModerationConfigurationDraft: test, activateModerationConfiguration: activate }));

		const result = await api.testDraft('draft-1', 'a benign sample');
		expect(result.decision).toBe('reject');
		expect(test).toHaveBeenCalledWith({ draftVersionId: 'draft-1', sampleComment: 'a benign sample' });

		const config = await api.activateDraft('draft-1', 'v-1');
		expect((config as ModerationConfiguration | undefined)?.versionId).toBe('v-2');
		expect(activate).toHaveBeenCalledWith({ draftVersionId: 'draft-1', expectedActiveVersionId: 'v-1' });
	});

	it('preserves API errors for the caller', async () => {
		const error = new ConnectError('rejected', Code.PermissionDenied);
		const api = new ConnectModerationConfigApi(
			makeClient({ getModerationSetupStatus: vi.fn(async () => { throw error; }) })
		);
		await expect(api.getSetupStatus()).rejects.toBe(error);
	});
});
