import { Code, ConnectError, type StreamRequest, type UnaryRequest, type UnaryResponse } from '@connectrpc/connect';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { goto } from '$app/navigation';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import { AuthService } from '$lib/services/auth/service.svelte';
import { authRedirectInterceptor } from './authRedirect';

vi.mock('$app/environment', () => ({ browser: true }));
vi.mock('$app/navigation', () => ({ goto: vi.fn(async () => undefined) }));

function setup() {
	const session = {
		accessToken: 'refreshed-token',
		user: { id: 'admin', email: 'admin@example.com', displayName: 'Admin', role: UserRole.ADMIN }
	};
	const api = {
		requestLink: vi.fn(async () => undefined),
		login: vi.fn(async () => session),
		register: vi.fn(async () => session),
		refresh: vi.fn(async () => session),
		logout: vi.fn(async () => undefined)
	};
	const auth = new AuthService(api);
	const request = {
		method: { name: 'ListAllowedDomains' },
		stream: false,
		header: new Headers()
	} as unknown as UnaryRequest;
	const response = { stream: false, header: new Headers(), trailer: new Headers() } as UnaryResponse;
	const next = vi.fn(async (_request: UnaryRequest | StreamRequest) => response);
	return { api, request, response, next, run: authRedirectInterceptor(auth.interceptor(next)) };
}

describe('authentication redirect transport interceptor', () => {
	beforeEach(() => vi.clearAllMocks());

	it('allows refresh and retry to recover before deciding to redirect', async () => {
		const { api, request, response, next, run } = setup();
		next.mockRejectedValueOnce(new ConnectError('expired token', Code.Unauthenticated));

		await expect(run(request)).resolves.toBe(response);
		expect(api.refresh).toHaveBeenCalledOnce();
		expect(next).toHaveBeenCalledTimes(2);
		expect(request.header.get('Authorization')).toBe('Bearer refreshed-token');
		expect(goto).not.toHaveBeenCalled();
	});

	it('redirects when refresh cannot restore the session and preserves the RPC error', async () => {
		const { api, request, next, run } = setup();
		const error = new ConnectError('authentication required', Code.Unauthenticated);
		next.mockRejectedValueOnce(error);
		api.refresh.mockRejectedValueOnce(new ConnectError('session expired', Code.Unauthenticated));

		await expect(run(request)).rejects.toBe(error);
		expect(api.refresh).toHaveBeenCalledOnce();
		expect(next).toHaveBeenCalledOnce();
		expect(goto).toHaveBeenCalledExactlyOnceWith('/login', { replaceState: true });
	});

	it('redirects if the retried request is still unauthenticated', async () => {
		const { api, request, next, run } = setup();
		const error = new ConnectError('token rejected', Code.Unauthenticated);
		next.mockRejectedValue(error);

		await expect(run(request)).rejects.toBe(error);
		expect(api.refresh).toHaveBeenCalledOnce();
		expect(next).toHaveBeenCalledTimes(2);
		expect(goto).toHaveBeenCalledExactlyOnceWith('/login', { replaceState: true });
	});

	it.each([Code.PermissionDenied, Code.Unavailable])('does not redirect for error code %s', async (code) => {
		const { api, request, next, run } = setup();
		const error = new ConnectError('request failed', code);
		next.mockRejectedValue(error);

		await expect(run(request)).rejects.toBe(error);
		expect(api.refresh).not.toHaveBeenCalled();
		expect(goto).not.toHaveBeenCalled();
	});
});
