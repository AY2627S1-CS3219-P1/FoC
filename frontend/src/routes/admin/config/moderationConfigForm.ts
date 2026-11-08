import { z } from 'zod';
import type { ModerationConfiguration } from '$lib/gen/moderation/v1/config_pb';
import { ModerationOutputMode } from '$lib/gen/moderation/v1/config_pb';
import { buildModerationParameters, ParameterValidationError, type ModerationParameterInput } from '$lib/services/moderation-config/parameters';

export const moderationConfigFormSchema = z.object({
	modelId: z.string().default(''),
	temperatureText: z.string().default(''), topPText: z.string().default(''),
	presencePenaltyText: z.string().default(''), frequencyPenaltyText: z.string().default(''),
	budgetKind: z.enum(['none', 'max_completion_tokens', 'max_tokens']).default('none'),
	budgetText: z.string().default(''), stopText: z.string().default(''),
	seedText: z.string().default(''), reasoningEffort: z.string().default(''),
	advancedText: z.string().default(''),
	outputMode: z.string().default(String(ModerationOutputMode.JSON_SCHEMA)),
	sampleComment: z.string().default('The delivery arrived on time. Thank you.'),
	resumePausedJobs: z.boolean().default(false)
});
export type ModerationConfigForm = z.infer<typeof moderationConfigFormSchema>;

export function hydrateModerationConfig(version: ModerationConfiguration): ModerationConfigForm {
	const p = version.parameters;
	return moderationConfigFormSchema.parse({
		modelId: version.modelId, outputMode: String(version.outputMode),
		temperatureText: p?.temperature?.toString() ?? '', topPText: p?.topP?.toString() ?? '',
		presencePenaltyText: p?.presencePenalty?.toString() ?? '', frequencyPenaltyText: p?.frequencyPenalty?.toString() ?? '',
		budgetKind: p?.maxCompletionTokens !== undefined ? 'max_completion_tokens' : p?.maxTokens !== undefined ? 'max_tokens' : 'none',
		budgetText: (p?.maxCompletionTokens ?? p?.maxTokens)?.toString() ?? '',
		stopText: JSON.stringify(p?.stop ?? []), seedText: p?.seed?.toString() ?? '',
		reasoningEffort: p?.reasoningEffort ?? '',
		advancedText: Object.entries(p?.advancedParameters ?? {}).map(([key, value]) => `${key}=${value}`).join('\n')
	});
}

function number(field: string, text: string): number | undefined {
	if (text.trim() === '') return undefined;
	const value = Number(text);
	if (!Number.isFinite(value)) throw new ParameterValidationError(field, `${field} must be a finite number.`);
	return value;
}
function integer(field: string, text: string): bigint | undefined {
	if (text.trim() === '') return undefined;
	if (!/^[-+]?\d+$/.test(text.trim())) throw new ParameterValidationError(field, `${field} must be a whole number.`);
	return BigInt(text.trim());
}

export function moderationParametersFromForm(form: ModerationConfigForm): ModerationParameterInput {
	const advanced: Record<string, string> = {};
	for (const line of form.advancedText.split('\n').map((line) => line.trim()).filter(Boolean)) {
		const index = line.indexOf('=');
		if (index <= 0) throw new ParameterValidationError('advancedParameters', 'Use name=value for advanced parameters.');
		const key = line.slice(0, index).trim();
		if (Object.hasOwn(advanced, key)) throw new ParameterValidationError('advancedParameters', 'Parameter names must be unique.');
		advanced[key] = line.slice(index + 1).trim();
	}
	let stop: string[] = [];
	try { stop = form.stopText.trim() === '' ? [] : JSON.parse(form.stopText); }
	catch { throw new ParameterValidationError('stop', 'Stop sequences must be a JSON array of strings.'); }
	if (!Array.isArray(stop) || stop.some((value) => typeof value !== 'string')) throw new ParameterValidationError('stop', 'Stop sequences must be strings.');
	const input: ModerationParameterInput = {
		temperature: number('temperature', form.temperatureText), topP: number('topP', form.topPText),
		presencePenalty: number('presencePenalty', form.presencePenaltyText), frequencyPenalty: number('frequencyPenalty', form.frequencyPenaltyText),
		maxCompletionTokens: form.budgetKind === 'max_completion_tokens' ? integer('budget', form.budgetText) : undefined,
		maxTokens: form.budgetKind === 'max_tokens' ? integer('budget', form.budgetText) : undefined,
		stop, seed: integer('seed', form.seedText), reasoningEffort: form.reasoningEffort.trim() || undefined,
		advancedParameters: advanced
	};
	buildModerationParameters(input);
	return input;
}
