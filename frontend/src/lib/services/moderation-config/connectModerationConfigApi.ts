import type { Client } from '@connectrpc/connect';
import {
	ModerationAdminService,
	type ModerationConfiguration,
	type ModerationModel,
	type ModerationOutputMode,
	type TestModerationConfigurationDraftResponse
} from '$lib/gen/moderation/v1/config_pb';
import type { ModerationConfigApi, ModerationSetupStatus } from './moderationConfigApi.ts';
import type { ModerationParameterInput } from './parameters.ts';
import { buildModerationParameters } from './parameters.ts';

// Thin transport adapter over the generated `ModerationAdminService`. It maps
// each RPC to the domain shape the service consumes and delegates parameter
// construction to [buildModerationParameters], which enforces the client-side
// allowlist. Errors are not swallowed: the service layer maps them.
export class ConnectModerationConfigApi implements ModerationConfigApi {
	readonly #client: Client<typeof ModerationAdminService>;

	constructor(client: Client<typeof ModerationAdminService>) {
		this.#client = client;
	}

	async getSetupStatus(): Promise<ModerationSetupStatus> {
		const response = await this.#client.getModerationSetupStatus({});
		return {
			hasActiveConfiguration: response.hasActiveConfiguration,
			hasProviderCredential: response.hasProviderCredential,
			providerFingerprint: response.providerFingerprint,
			policyVersion: response.policyVersion,
 defaultSystemMessage: response.defaultSystemMessage
		};
	}

	async listModels(): Promise<ModerationModel[]> {
		const response = await this.#client.listAvailableModels({});
		return response.models;
	}

	async getActiveConfiguration(): Promise<ModerationConfiguration | undefined> {
		const response = await this.#client.getActiveModerationConfiguration({});
		return response.configuration;
	}

	async saveDraft(
		modelId: string,
		parameters: ModerationParameterInput,
		outputMode: ModerationOutputMode,
 systemMessage = ''
	): Promise<string> {
		const response = await this.#client.saveModerationConfigurationDraft({
			modelId,
			parameters: buildModerationParameters(parameters),
			outputMode, systemMessage
		});
		return response.draftVersionId;
	}

	async testDraft(
		draftVersionId: string,
		sampleComment: string
	): Promise<TestModerationConfigurationDraftResponse> {
		const response = await this.#client.testModerationConfigurationDraft({ draftVersionId, sampleComment });
		return response;
	}

	async activateDraft(
		draftVersionId: string,
		expectedActiveVersionId: string,
		resumePausedJobs = false
	): Promise<ModerationConfiguration | undefined> {
		const response = await this.#client.activateModerationConfiguration({
			draftVersionId,
			expectedActiveVersionId,
			resumePausedJobs
		});
		return response.configuration;
	}
}
