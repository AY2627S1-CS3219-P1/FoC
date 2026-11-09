import type {
	ModerationConfiguration,
	ModerationModel,
	ModerationOutputMode,
	TestModerationConfigurationDraftResponse
} from '$lib/gen/moderation/v1/config_pb';
import type { ModerationParameterInput } from './parameters.ts';

// Domain view of the setup status: it mirrors the generated response but is
// declared here because the service owns the fields the UI renders and may
// reshape them without disturbing the transport.
export interface ModerationSetupStatus {
	hasActiveConfiguration: boolean;
	hasProviderCredential: boolean;
	providerFingerprint: string;
	policyVersion: string;
}

export interface ModerationConfigApi {
	getSetupStatus(): Promise<ModerationSetupStatus>;
	listModels(): Promise<ModerationModel[]>;
	getActiveConfiguration(): Promise<ModerationConfiguration | undefined>;
	saveDraft(modelId: string, parameters: ModerationParameterInput, outputMode: ModerationOutputMode): Promise<string>;
	testDraft(draftVersionId: string, sampleComment: string): Promise<TestModerationConfigurationDraftResponse>;
	activateDraft(draftVersionId: string, expectedActiveVersionId: string, resumePausedJobs?: boolean): Promise<ModerationConfiguration | undefined>;
}
