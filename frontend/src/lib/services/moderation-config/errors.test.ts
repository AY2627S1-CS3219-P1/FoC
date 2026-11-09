import { describe, expect, it } from 'vitest';
import { Code, ConnectError } from '@connectrpc/connect';
import { create, toBinary } from '@bufbuild/protobuf';
import { ViolationsSchema } from '$lib/gen/buf/validate/validate_pb';
import { describeModerationConfigError, extractValidationMessages } from './errors.ts';

describe('describeModerationConfigError', () => {
	it.each([
		[Code.Unauthenticated, 'Your session has expired. Please sign in again.'],
		[Code.PermissionDenied, 'You do not have permission to change the moderation configuration.'],
		[Code.NotFound, 'This configuration could not be found.'],
		[Code.InvalidArgument, 'One or more settings were rejected. Check the highlighted fields and try again.'],
		[Code.Aborted, 'The active configuration changed while you were editing. Reload and try again.'],
		[Code.FailedPrecondition, 'The active configuration changed while you were editing. Reload and try again.'],
		[Code.Internal, 'The operation could not be completed. Check your connection and try again.']
	] as const)('maps %s to a stable message', (code, message) => {
		expect(describeModerationConfigError(new ConnectError('x', code))).toBe(message);
	});

	it('maps a thrown non-connect error to the generic message', () => {
		expect(describeModerationConfigError(new Error('boom'))).toBe(
			'The operation could not be completed. Check your connection and try again.'
		);
	});
});

describe('extractValidationMessages', () => {
	// Build the detail the wire client produces: { type, value: bytes }.
	function withViolation(code: Code, message: string): ConnectError {
		const error = new ConnectError('validation failed', code);
		const violations = create(ViolationsSchema, { violations: [{ message }] });
		error.details.push({ type: ViolationsSchema.typeName, value: toBinary(ViolationsSchema, violations) } as never);
		return error;
	}

	it('returns the server violation messages for an InvalidArgument error', () => {
		const error = withViolation(
			Code.InvalidArgument,
			'temperature must be within 0 and 2.'
		);
		expect(extractValidationMessages(error)).toEqual(['temperature must be within 0 and 2.']);
	});

	it('returns nothing when the server sends no protovalidate detail', () => {
		expect(extractValidationMessages(new ConnectError('bad', Code.InvalidArgument))).toEqual([]);
	});

	it('returns nothing for a non-validation failure', () => {
		expect(extractValidationMessages(new ConnectError('nope', Code.PermissionDenied))).toEqual([]);
		expect(extractValidationMessages(new Error('plain'))).toEqual([]);
	});

	it('does not leak the raw ConnectError message into the domain error', () => {
		const message = describeModerationConfigError(withViolation(Code.InvalidArgument, 'secret field rule'));
		expect(message).not.toContain('secret field rule');
	});
});
