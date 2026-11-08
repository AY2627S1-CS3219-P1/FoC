import { describe, expect, it } from 'vitest';
import { create, toJson } from '@bufbuild/protobuf';
import { ModerationConfigurationSchema, ModerationInferenceParametersSchema } from '$lib/gen/moderation/v1/config_pb';
import { hydrateModerationConfig, moderationParametersFromForm, moderationConfigFormSchema } from './moderationConfigForm';
import { buildModerationParameters } from '$lib/services/moderation-config/parameters';

describe('editing an existing moderation configuration', () => {
	it.each([{}, {
		temperature: 0, topP: 0.9, presencePenalty: -0.2, frequencyPenalty: 0.3,
		maxCompletionTokens: 1234n, seed: 9007199254740993n,
		stop: ['END,KEEP COMMA', ' other '], reasoningEffort: 'high', advancedParameters: { top_k: '40' }
	}, {maxTokens: 321n}])('round trips all settings and optional presence', (settings) => {
		const parameters = create(ModerationInferenceParametersSchema, settings);
		const version = create(ModerationConfigurationSchema, { modelId: 'model', parameters });
		const form = hydrateModerationConfig(version);
		const saved = buildModerationParameters(moderationParametersFromForm(form));
		expect(toJson(ModerationInferenceParametersSchema, saved)).toEqual(toJson(ModerationInferenceParametersSchema, parameters));
	});
	it('does not silently discard an invalid numeric field', () => {
		const form = moderationConfigFormSchema.parse({temperatureText: 'abc'});
		expect(() => moderationParametersFromForm(form)).toThrow('finite number');
	});
});
