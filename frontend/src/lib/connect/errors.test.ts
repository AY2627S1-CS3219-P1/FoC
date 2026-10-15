import { Code, ConnectError, createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { describe, expect, it } from 'vitest';
import { AllowedDomainAdminService } from '$lib/gen/user/v1/admin_pb';
import { ConnectAllowedDomainApi } from '$lib/services/allowedDomain/connectAllowedDomainApi';
import { getApiErrorMessage } from './errors.ts';

const fallback = 'We could not complete the request. Please try again.';

describe('getApiErrorMessage', () => {
	it.each([
		Code.AlreadyExists, Code.InvalidArgument, Code.NotFound, Code.PermissionDenied,
		Code.Unauthenticated, Code.Aborted, Code.FailedPrecondition, Code.Unavailable,
		Code.Internal, Code.Unknown, Code.DeadlineExceeded, Code.Canceled
	])('preserves the backend message for Connect code %s without its code prefix', (code) => {
		const error = new ConnectError('Backend explanation for this request.', code);
		expect(getApiErrorMessage(error)).toBe('Backend explanation for this request.');
	});

	it.each(['', ' ', '\n\t'])('falls back for a blank backend message %j', (message) => {
		expect(getApiErrorMessage(new ConnectError(message, Code.InvalidArgument))).toBe(fallback);
	});

	it.each([new Error('private implementation detail'), new TypeError('Failed to fetch'), null, undefined, 'unexpected rejection'])(
		'falls back for local or unexpected errors (%s)', (error) => {
			expect(getApiErrorMessage(error)).toBe(fallback);
		}
	);

	it('falls back when Connect wraps a local fetch failure', () => {
		expect(getApiErrorMessage(ConnectError.from(new TypeError('Failed to fetch')))).toBe(fallback);
	});

	it('preserves a backend error through a generated client and the domain adapter', async () => {
		const transport = createConnectTransport({
			baseUrl: 'http://localhost',
			useBinaryFormat: false,
			fetch: async () => new Response(JSON.stringify({
				code: 'already_exists', message: 'This email domain is already allowed.'
			}), { status: 409, headers: { 'Content-Type': 'application/json' } })
		});
		const api = new ConnectAllowedDomainApi(createClient(AllowedDomainAdminService, transport));
		const error = await api.allowDomain('example.com').catch((cause: unknown) => cause);
		expect(error).toBeInstanceOf(ConnectError);
		expect((error as ConnectError).code).toBe(Code.AlreadyExists);
		expect(getApiErrorMessage(error)).toBe('This email domain is already allowed.');
	});

	it('uses the fallback for a real client-side fetch rejection', async () => {
		const client = createClient(AllowedDomainAdminService, createConnectTransport({
			baseUrl: 'http://localhost',
			useBinaryFormat: false,
			fetch: async () => { throw new TypeError('Failed to fetch'); }
		}));
		const error = await client.listAllowedDomains({}).catch((cause: unknown) => cause);
		expect(error).toBeInstanceOf(ConnectError);
		expect(getApiErrorMessage(error)).toBe(fallback);
	});

	it('treats an empty RPC response as success without requiring a backend message', async () => {
		const api = new ConnectAllowedDomainApi(createClient(AllowedDomainAdminService, createConnectTransport({
			baseUrl: 'http://localhost',
			useBinaryFormat: false,
			fetch: async () => new Response('{}', { headers: { 'Content-Type': 'application/json' } })
		})));
		await expect(api.allowDomain('example.com')).resolves.toBeUndefined();
	});
});
