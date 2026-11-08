import { describe, expect, it } from 'vitest';
import type { ModerationParameterInput } from './parameters.ts';
import {
	ParameterValidationError,
	buildModerationParameters,
	RESERVED_REQUEST_FIELDS
} from './parameters.ts';

describe('buildModerationParameters', () => {
	it('maps every provided field, including the int64 token and seed spellings', () => {
		const input: ModerationParameterInput = {
			temperature: 0.5,
			topP: 0.9,
			presencePenalty: 0.1,
			frequencyPenalty: 0.2,
			maxCompletionTokens: 256,
			seed: 42n,
			stop: ['END', 'END', '', '   '],
			reasoningEffort: 'high',
			advancedParameters: { 'some_key': 'v' }
		};
		const params = buildModerationParameters(input);

		expect(params.temperature).toBe(0.5);
		expect(params.topP).toBe(0.9);
		expect(params.presencePenalty).toBe(0.1);
		expect(params.frequencyPenalty).toBe(0.2);
		expect(params.maxCompletionTokens).toBe(256n);
		expect(params.seed).toBe(42n);
		expect(params.reasoningEffort).toBe('high');
		// Stop sequences are trimmed, de-duplicated and have empties dropped.
		expect(params.stop).toEqual(['END']);
		expect(params.advancedParameters).toEqual({ some_key: 'v' });
	});

	it('omits every field left unset, so zero does not win a provider default', () => {
		const params = buildModerationParameters({});
		expect(params.maxCompletionTokens).toBeUndefined();
		expect(params.maxTokens).toBeUndefined();
		expect(params.seed).toBeUndefined();
		expect(params.reasoningEffort).toBeUndefined();
		expect(params.stop).toEqual([]);
		expect(params.advancedParameters).toEqual({});
	});

	it.each([...RESERVED_REQUEST_FIELDS])('rejects a reserved advanced parameter %s before sending', (key) => {
		expect(() => buildModerationParameters({ advancedParameters: { [key]: 'x' } })).toThrow(ParameterValidationError);
	});

	it('rejects an empty advanced parameter name', () => {
		expect(() => buildModerationParameters({ advancedParameters: { '': 'x' } })).toThrow(ParameterValidationError);
	});

	it('rejects setting both token budget spellings at once', () => {
		expect(() => buildModerationParameters({ maxCompletionTokens: 100, maxTokens: 100 })).toThrow(ParameterValidationError);
	});

	it('accepts a single token budget spelling', () => {
		expect(() => buildModerationParameters({ maxCompletionTokens: 100 })).not.toThrow();
		expect(() => buildModerationParameters({ maxTokens: 100 })).not.toThrow();
	});

	it('converts a whole-number bigint and rejects a fractional number', () => {
		expect(buildModerationParameters({ maxTokens: 128 }).maxTokens).toBe(128n);
		expect(() => buildModerationParameters({ maxTokens: 1.5 })).toThrow(ParameterValidationError);
		expect(() => buildModerationParameters({ seed: Number.NaN })).toThrow(ParameterValidationError);
	});
});
