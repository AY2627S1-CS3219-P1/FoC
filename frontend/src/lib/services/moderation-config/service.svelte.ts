import { ModerationOutputMode } from '$lib/gen/moderation/v1/config_pb';
import type {
	ModerationModel,
	ModerationConfiguration,
	TestModerationConfigurationDraftResponse
} from '$lib/gen/moderation/v1/config_pb';
import type { ModerationConfigApi } from './moderationConfigApi.ts';
import type { ModerationSetupStatus } from './moderationConfigApi.ts';
import type { ModerationParameterInput } from './parameters.ts';

// The set of operations the service tracks loading state for. Declared once so
// the type, the state record and the reset loop stay in lock-step.
export const LOADING_KEYS = ['setup', 'models', 'active', 'save', 'test', 'activate'] as const;
export type LoadingKey = (typeof LOADING_KEYS)[number];

// Rune-state wrapper over [ModerationConfigApi]. It owns the loaded configuration
// data and per-operation loading/error state so the admin route can render the
// setup banner, model picker and current config without repeating fetch handling.
export class ModerationConfigService {
	readonly #api: ModerationConfigApi;

	#setup = $state<ModerationSetupStatus | undefined>(undefined);
	#models = $state<ModerationModel[]>([]);
	#activeConfiguration = $state<ModerationConfiguration | undefined>(undefined);
	#testResult = $state<TestModerationConfigurationDraftResponse | undefined>(undefined);

	#loading = $state<Record<LoadingKey, boolean>>({
		setup: false,
		models: false,
		active: false,
		save: false,
		test: false,
		activate: false
	});
	#error = $state<unknown>(null);

	constructor(api: ModerationConfigApi) {
		this.#api = api;
	}

	get setup(): ModerationSetupStatus | undefined {
		return this.#setup;
	}

	get models(): ModerationModel[] {
		return this.#models;
	}

	get activeConfiguration(): ModerationConfiguration | undefined {
		return this.#activeConfiguration;
	}

	get testResult(): TestModerationConfigurationDraftResponse | undefined {
		return this.#testResult;
	}

	get error(): unknown {
		return this.#error;
	}

	get isBusy(): boolean {
		return Object.values(this.#loading).some((busy) => busy);
	}

	isLoading(key: LoadingKey): boolean {
		return this.#loading[key] ?? false;
	}

	async loadSetup(): Promise<void> {
		this.#setLoading('setup', true);
		this.#error = null;
		try {
			this.#setup = await this.#api.getSetupStatus();
		} catch (error) {
			this.#error = error;
			throw error;
		} finally {
			this.#setLoading('setup', false);
		}
	}

	async loadModels(): Promise<void> {
		this.#setLoading('models', true);
		this.#error = null;
		try {
			this.#models = await this.#api.listModels();
		} catch (error) {
			this.#error = error;
			throw error;
		} finally {
			this.#setLoading('models', false);
		}
	}

	async loadActiveConfiguration(): Promise<void> {
		this.#setLoading('active', true);
		this.#error = null;
		try {
			this.#activeConfiguration = await this.#api.getActiveConfiguration();
		} catch (error) {
			this.#error = error;
			throw error;
		} finally {
			this.#setLoading('active', false);
		}
	}

	// Convenience so a route can populate the three independent reads at once.
	async loadAll(): Promise<void> {
		await Promise.all([this.loadSetup(), this.loadModels(), this.loadActiveConfiguration()]);
	}

	async saveDraft(
		modelId: string,
		parameters: ModerationParameterInput,
		outputMode: ModerationOutputMode
	): Promise<string> {
		this.#setLoading('save', true);
		this.#error = null;
		try {
			return await this.#api.saveDraft(modelId, parameters, outputMode);
		} catch (error) {
			this.#error = error;
			throw error;
		} finally {
			this.#setLoading('save', false);
		}
	}

	async testDraft(draftVersionId: string, sampleComment: string): Promise<TestModerationConfigurationDraftResponse> {
		this.#setLoading('test', true);
		this.#error = null;
		try {
			const result = await this.#api.testDraft(draftVersionId, sampleComment);
			this.#testResult = result;
			return result;
		} catch (error) {
			this.#error = error;
			throw error;
		} finally {
			this.#setLoading('test', false);
		}
	}

	async activateDraft(draftVersionId: string, expectedActiveVersionId: string): Promise<ModerationConfiguration | undefined> {
		this.#setLoading('activate', true);
		this.#error = null;
		try {
			const configuration = await this.#api.activateDraft(draftVersionId, expectedActiveVersionId);
			// Reflect the newly activated configuration in the loaded state.
			this.#activeConfiguration = configuration;
			return configuration;
		} catch (error) {
			this.#error = error;
			throw error;
		} finally {
			this.#setLoading('activate', false);
		}
	}

	reset(): void {
		this.#setup = undefined;
		this.#models = [];
		this.#activeConfiguration = undefined;
		this.#testResult = undefined;
		this.#error = null;
		for (const key of LOADING_KEYS) {
			this.#loading[key] = false;
		}
	}

	#setLoading(key: LoadingKey, value: boolean): void {
		this.#loading[key] = value;
	}
}
