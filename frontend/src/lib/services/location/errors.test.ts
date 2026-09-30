import { Code, ConnectError } from '@connectrpc/connect';
import { describe, expect, it } from 'vitest';
import { describeLocationError } from './errors.ts';

describe('describeLocationError', () => {
	it.each([
		[Code.Unauthenticated, 'Please log in to see campus locations.'],
		[Code.PermissionDenied, 'You do not have access to this view.'],
		[Code.NotFound, 'This location does not exist.'],
		[Code.InvalidArgument, 'Some search options are invalid. Try clearing the filters.'],
		[Code.Internal, 'Could not load locations. Please try again.']
	])('maps Connect code %s', (code, message) => {
		expect(describeLocationError(new ConnectError('boom', code))).toBe(message);
	});

	it('falls back for non-Connect errors such as network failures', () => {
		expect(describeLocationError(new TypeError('Failed to fetch'))).toBe(
			'Could not load locations. Please try again.'
		);
	});
});
