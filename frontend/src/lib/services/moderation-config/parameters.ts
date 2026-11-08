import { create } from '@bufbuild/protobuf';
import { ModerationInferenceParametersSchema } from '$lib/gen/moderation/v1/config_pb';
import type { ModerationInferenceParameters } from '$lib/gen/moderation/v1/config_pb';

/**
 * Moderation inference parameters an administrator may edit in a draft. Every
 * field is optional so the admin can change just the subset they care about;
 * the builder below converts this into the generated proto message, skipping
 * anything that was left unset.
 */
export interface ModerationParameterInput {
	temperature?: number;
	topP?: number;
	presencePenalty?: number;
	frequencyPenalty?: number;
	// Mutually exclusive token budget spellings; only one may be set.
	maxCompletionTokens?: number | bigint;
	maxTokens?: number | bigint;
	seed?: number | bigint;
	stop?: string[];
	reasoningEffort?: string;
	// Extra provider parameters. Subject to a service-owned allowlist enforced
	// here and again server-side; application-owned request fields can never be
	// overridden through it.
	advancedParameters?: { [key: string]: string };
}

/**
 * Provider request fields the advanced-parameters map may never override. These
 * are service-owned: the model, the prompts, roles, the output schema, the
 * endpoint and credentials are all fixed by the moderation policy, so a draft
 * that tries to touch them is rejected before it is sent. Mirrors the set the
 * OpenAI adapter treats as reserved.
 */
export const RESERVED_REQUEST_FIELDS = [
	'model',
	'messages',
	'system',
	'user',
	'response_format'
] as const;

const RESERVED_FIELD_SET = new Set<string>(RESERVED_REQUEST_FIELDS);

/** Thrown when a draft's parameters are rejected by the client-side allowlist or shape rules. */
export class ParameterValidationError extends Error {
	constructor(readonly field: string, message: string) {
		super(message);
		this.name = 'ParameterValidationError';
	}
}

function normalizeStop(stop: string[]): string[] {
	return [...stop];
}

function toInt64(field: string, value: number | bigint): bigint {
	if (typeof value === 'bigint') return value;
	if (!Number.isFinite(value) || !Number.isInteger(value)) {
		throw new ParameterValidationError(field, `${field} must be a whole number.`);
	}
	return BigInt(value);
}

/**
 * Build the generated `ModerationInferenceParameters` for a draft, preserving
 * the contract the server expects. Fields left unset are omitted, so zero does
 * not overwrite a provider default the admin did not intend to change.
 *
 * It enforces two client-side rules the server also guards, failing fast with a
 * typed error the UI can surface directly:
 *  - advanced parameters may not override a reserved service-owned field;
 *  - `maxCompletionTokens` and `maxTokens` are mutually exclusive.
 *
 * Semantic bounds (ranges, non-empty sample, etc.) are left to the server's
 * own validation and surfaced through [describeModerationConfigError].
 */
export function buildModerationParameters(input: ModerationParameterInput): ModerationInferenceParameters {
	validateAdvanced(input.advancedParameters);

	const hasBothBudgets = input.maxCompletionTokens !== undefined && input.maxTokens !== undefined;
	if (hasBothBudgets) {
		throw new ParameterValidationError(
			'maxTokens',
			'Set only one of maxCompletionTokens or maxTokens.'
		);
	}

	const params = create(ModerationInferenceParametersSchema, {
		temperature: input.temperature,
		topP: input.topP,
		presencePenalty: input.presencePenalty,
		frequencyPenalty: input.frequencyPenalty,
		stop: input.stop ? normalizeStop(input.stop) : [],
		advancedParameters: { ...(input.advancedParameters ?? {}) }
	});

	if (input.maxCompletionTokens !== undefined) params.maxCompletionTokens = toInt64('maxCompletionTokens', input.maxCompletionTokens);
	if (input.maxTokens !== undefined) params.maxTokens = toInt64('maxTokens', input.maxTokens);
	if (input.seed !== undefined) params.seed = toInt64('seed', input.seed);
	if (input.reasoningEffort !== undefined && input.reasoningEffort.trim() !== '') {
		params.reasoningEffort = input.reasoningEffort.trim();
	}
	return params;
}

// Reject reserved and otherwise unusable advanced parameter keys before a
// round trip, so the admin gets a clear reason rather than a generic server
// InvalidArgument for something the UI can prevent.
function validateAdvanced(advanced: { [key: string]: string } | undefined): void {
	if (!advanced) return;
	for (const key of Object.keys(advanced)) {
		if (key.length === 0) {
			throw new ParameterValidationError('advancedParameters', 'Parameter names must not be empty.');
		}
		if (RESERVED_FIELD_SET.has(key)) {
			throw new ParameterValidationError(
				'advancedParameters',
				`"${key}" is a system-owned field and cannot be overridden.`
			);
		}
	}
}
