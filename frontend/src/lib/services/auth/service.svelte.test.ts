import {
	Code,
	ConnectError,
	type StreamRequest,
	type UnaryRequest,
	type UnaryResponse
} from '@connectrpc/connect';
import { describe, expect, it, vi } from 'vitest';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
import type { AuthApi, AuthSession } from './authApi.ts';
import { AuthService } from './service.svelte.ts';

function createSession(accessToken = 'access-token'): AuthSession {
	return {
		accessToken,
		user: {
			id: 'user-1',
			email: 'user@example.com',
			displayName: 'Test User',
			role: UserRole.USER
		}
	};
}

function createAuthApi() {
	return {
		requestLink: vi.fn(async (_email: string) => undefined),
		login: vi.fn(async (_token: string) => createSession()),
		register: vi.fn(async (_token: string, _displayName: string) => createSession()),
		refresh: vi.fn(async () => createSession()),
		logout: vi.fn(async () => undefined)
	} satisfies AuthApi;
}

function deferred<T>() {
	let resolve!: (value: T | PromiseLike<T>) => void;
	let reject!: (reason?: unknown) => void;
	const promise = new Promise<T>((resolvePromise, rejectPromise) => {
		resolve = resolvePromise;
		reject = rejectPromise;
	});
	return { promise, resolve, reject };
}

function createUnaryRequest(): UnaryRequest {
	return {
		stream: false,
		header: new Headers()
	} as unknown as UnaryRequest;
}

function createStreamRequest(): StreamRequest {
	return {
		stream: true,
		header: new Headers()
	} as unknown as StreamRequest;
}

function createUnaryResponse(): UnaryResponse {
	return {
		stream: false,
		header: new Headers(),
		trailer: new Headers()
	} as unknown as UnaryResponse;
}

describe('AuthService', () => {
	it('starts without an authenticated session', () => {
		const service = new AuthService(createAuthApi());

		expect(service.user).toBeNull();
		expect(service.accessToken).toBeNull();
		expect(service.isAuthenticated).toBe(false);
		expect(service.isLoading).toBe(false);
	});

	it('delegates request-link without changing session state', async () => {
		const api = createAuthApi();
		const service = new AuthService(api);

		await service.requestLink('student@example.com');

		expect(api.requestLink).toHaveBeenCalledOnce();
		expect(api.requestLink).toHaveBeenCalledWith('student@example.com');
		expect(service.isAuthenticated).toBe(false);
	});

	it('stores the session returned by login', async () => {
		const api = createAuthApi();
		const session = createSession('login-token');
		api.login.mockResolvedValueOnce(session);
		const service = new AuthService(api);

		await service.login('magic-link-token');

		expect(api.login).toHaveBeenCalledWith('magic-link-token');
		expect(service.accessToken).toBe('login-token');
		expect(service.user).toEqual(session.user);
		expect(service.isAuthenticated).toBe(true);
	});

	it('updates the signed-in display name after a profile save', async () => {
		const api = createAuthApi();
		api.login.mockResolvedValueOnce(createSession());
		const service = new AuthService(api);
		await service.login('valid-token');

		service.updateDisplayName('Updated Name');

		expect(service.user?.displayName).toBe('Updated Name');
		expect(service.accessToken).toBe('access-token');
	});

	it('stores the session returned by registration', async () => {
		const api = createAuthApi();
		const session = createSession('registration-token');
		api.register.mockResolvedValueOnce(session);
		const service = new AuthService(api);

		await service.register('magic-link-token', 'New User');

		expect(api.register).toHaveBeenCalledWith('magic-link-token', 'New User');
		expect(service.accessToken).toBe('registration-token');
		expect(service.user).toEqual(session.user);
	});

	it('preserves an existing session when login fails', async () => {
		const api = createAuthApi();
		const existingSession = createSession('existing-token');
		api.login.mockResolvedValueOnce(existingSession);
		const service = new AuthService(api);
		await service.login('valid-token');
		const error = new Error('login failed');
		api.login.mockRejectedValueOnce(error);

		await expect(service.login('invalid-token')).rejects.toBe(error);

		expect(service.accessToken).toBe('existing-token');
		expect(service.user).toEqual(existingSession.user);
	});

	it('preserves an existing session when registration fails', async () => {
		const api = createAuthApi();
		const existingSession = createSession('existing-token');
		api.login.mockResolvedValueOnce(existingSession);
		const service = new AuthService(api);
		await service.login('valid-token');
		const error = new Error('registration failed');
		api.register.mockRejectedValueOnce(error);

		await expect(service.register('invalid-token', 'New User')).rejects.toBe(error);

		expect(service.accessToken).toBe('existing-token');
		expect(service.user).toEqual(existingSession.user);
	});

	describe('refresh', () => {
		it('sets loading while pending and stores the refreshed session', async () => {
			const api = createAuthApi();
			const pending = deferred<AuthSession>();
			api.refresh.mockReturnValueOnce(pending.promise);
			const service = new AuthService(api);

			const refresh = service.refresh();

			expect(service.isLoading).toBe(true);
			await vi.waitFor(() => expect(api.refresh).toHaveBeenCalledOnce());
			pending.resolve(createSession('refreshed-token'));
			await refresh;

			expect(service.accessToken).toBe('refreshed-token');
			expect(service.isAuthenticated).toBe(true);
			expect(service.isLoading).toBe(false);
		});

		it('shares one API request between concurrent refreshes and permits a later refresh', async () => {
			const api = createAuthApi();
			const pending = deferred<AuthSession>();
			api.refresh.mockReturnValueOnce(pending.promise);
			const service = new AuthService(api);

			const refreshes = [service.refresh(), service.refresh(), service.refresh()];
			await vi.waitFor(() => expect(api.refresh).toHaveBeenCalledOnce());
			pending.resolve(createSession('shared-token'));
			await Promise.all(refreshes);

			expect(api.refresh).toHaveBeenCalledTimes(1);

			api.refresh.mockResolvedValueOnce(createSession('later-token'));
			await service.refresh();

			expect(api.refresh).toHaveBeenCalledTimes(2);
			expect(service.accessToken).toBe('later-token');
		});

		it('clears the session and loading state when refresh credentials are rejected', async () => {
			const api = createAuthApi();
			const service = new AuthService(api);
			await service.login('valid-token');
			const error = new ConnectError('refresh credentials rejected', Code.Unauthenticated);
			api.refresh.mockRejectedValueOnce(error);

			await expect(service.refresh()).rejects.toBe(error);

			expect(service.user).toBeNull();
			expect(service.accessToken).toBeNull();
			expect(service.isAuthenticated).toBe(false);
			expect(service.isLoading).toBe(false);

			api.refresh.mockResolvedValueOnce(createSession('recovered-token'));
			await service.refresh();

			expect(api.refresh).toHaveBeenCalledTimes(2);
			expect(service.accessToken).toBe('recovered-token');
		});

		it.each([
			new ConnectError('temporarily unavailable', Code.Unavailable),
			new Error('network failure')
		])('preserves an existing session when refresh fails transiently: %s', async (error) => {
			const api = createAuthApi();
			const session = createSession('existing-token');
			api.login.mockResolvedValueOnce(session);
			const service = new AuthService(api);
			await service.login('valid-token');
			api.refresh.mockRejectedValueOnce(error);

			await expect(service.refresh()).rejects.toBe(error);

			expect(service.accessToken).toBe(session.accessToken);
			expect(service.user).toEqual(session.user);
			expect(service.isAuthenticated).toBe(true);
			expect(service.isLoading).toBe(false);
		});

		it('allows restoreSession to absorb refresh failure', async () => {
			const api = createAuthApi();
			api.refresh.mockRejectedValueOnce(new Error('no session'));
			const service = new AuthService(api);

			await expect(service.restoreSession()).resolves.toBeUndefined();
			expect(api.refresh).toHaveBeenCalledOnce();
			expect(service.isAuthenticated).toBe(false);
		});
	});

	describe('interceptor', () => {
		it('adds the current access token and returns a successful response', async () => {
			const api = createAuthApi();
			api.login.mockResolvedValueOnce(createSession('current-token'));
			const service = new AuthService(api);
			await service.login('valid-token');
			const request = createUnaryRequest();
			const response = createUnaryResponse();
			const next = vi.fn(async () => response);

			await expect(service.interceptor(next)(request)).resolves.toBe(response);

			expect(next).toHaveBeenCalledOnce();
			expect(request.header.get('Authorization')).toBe('Bearer current-token');
			expect(api.refresh).not.toHaveBeenCalled();
		});

		it('rethrows non-authentication errors without refreshing', async () => {
			const api = createAuthApi();
			const service = new AuthService(api);
			const request = createUnaryRequest();
			const error = new ConnectError('unavailable', Code.Unavailable);
			const next = vi.fn(async () => {
				throw error;
			});

			await expect(service.interceptor(next)(request)).rejects.toBe(error);

			expect(next).toHaveBeenCalledOnce();
			expect(api.refresh).not.toHaveBeenCalled();
		});

		it('refreshes and retries an unauthenticated unary request with the new token', async () => {
			const api = createAuthApi();
			api.login.mockResolvedValueOnce(createSession('expired-token'));
			api.refresh.mockResolvedValueOnce(createSession('fresh-token'));
			const service = new AuthService(api);
			await service.login('valid-token');
			const request = createUnaryRequest();
			const originalError = new ConnectError('expired', Code.Unauthenticated);
			const response = createUnaryResponse();
			const next = vi
				.fn()
				.mockRejectedValueOnce(originalError)
				.mockResolvedValueOnce(response);

			await expect(service.interceptor(next)(request)).resolves.toBe(response);

			expect(api.refresh).toHaveBeenCalledOnce();
			expect(next).toHaveBeenCalledTimes(2);
			expect(request.header.get('Authorization')).toBe('Bearer fresh-token');
		});

		it('shares one refresh when concurrent requests are unauthenticated', async () => {
			const api = createAuthApi();
			const pending = deferred<AuthSession>();
			api.refresh.mockReturnValueOnce(pending.promise);
			const service = new AuthService(api);
			const firstRequest = createUnaryRequest();
			const secondRequest = createUnaryRequest();
			const attempts = new WeakMap<object, number>();
			const response = createUnaryResponse();
			const next = vi.fn(async (request: UnaryRequest | StreamRequest) => {
				const attempt = (attempts.get(request) ?? 0) + 1;
				attempts.set(request, attempt);
				if (attempt === 1) {
					throw new ConnectError('expired', Code.Unauthenticated);
				}
				return response;
			});
			const intercepted = service.interceptor(next);

			const first = intercepted(firstRequest);
			const second = intercepted(secondRequest);
			await vi.waitFor(() => expect(api.refresh).toHaveBeenCalledOnce());
			pending.resolve(createSession('shared-token'));
			await Promise.all([first, second]);

			expect(api.refresh).toHaveBeenCalledTimes(1);
			expect(attempts.get(firstRequest)).toBe(2);
			expect(attempts.get(secondRequest)).toBe(2);
			expect(firstRequest.header.get('Authorization')).toBe('Bearer shared-token');
			expect(secondRequest.header.get('Authorization')).toBe('Bearer shared-token');
		});

		it('rethrows the original error without retrying when refresh fails', async () => {
			const api = createAuthApi();
			api.refresh.mockRejectedValueOnce(new Error('refresh failed'));
			const service = new AuthService(api);
			const request = createUnaryRequest();
			const originalError = new ConnectError('expired', Code.Unauthenticated);
			const next = vi.fn(async () => {
				throw originalError;
			});

			await expect(service.interceptor(next)(request)).rejects.toBe(originalError);

			expect(api.refresh).toHaveBeenCalledOnce();
			expect(next).toHaveBeenCalledOnce();
		});

		it('clears the session when the retried request is still unauthenticated', async () => {
			const api = createAuthApi();
			api.login.mockResolvedValueOnce(createSession('expired-token'));
			api.refresh.mockResolvedValueOnce(createSession('fresh-token'));
			const service = new AuthService(api);
			await service.login('valid-token');
			const originalError = new ConnectError('expired', Code.Unauthenticated);
			const retryError = new ConnectError('still expired', Code.Unauthenticated);
			const next = vi
				.fn()
				.mockRejectedValueOnce(originalError)
				.mockRejectedValueOnce(retryError);

			await expect(service.interceptor(next)(createUnaryRequest())).rejects.toBe(retryError);

			expect(service.user).toBeNull();
			expect(service.accessToken).toBeNull();
			expect(service.isAuthenticated).toBe(false);
		});

		it('refreshes but does not retry an unauthenticated streaming request', async () => {
			const api = createAuthApi();
			api.refresh.mockResolvedValueOnce(createSession('fresh-token'));
			const service = new AuthService(api);
			const originalError = new ConnectError('expired', Code.Unauthenticated);
			const next = vi.fn(async () => {
				throw originalError;
			});

			await expect(service.interceptor(next)(createStreamRequest())).rejects.toBe(originalError);

			expect(api.refresh).toHaveBeenCalledOnce();
			expect(next).toHaveBeenCalledOnce();
		});
	});

	describe('logout', () => {
		it('clears the session and sends every logout invocation', async () => {
			const api = createAuthApi();
			const service = new AuthService(api);
			await service.login('valid-token');

			await Promise.all([service.logout(), service.logout()]);

			expect(api.logout).toHaveBeenCalledTimes(2);
			expect(service.user).toBeNull();
			expect(service.accessToken).toBeNull();
			expect(service.isAuthenticated).toBe(false);
		});

		it('prevents an earlier in-flight login from restoring the session', async () => {
			const api = createAuthApi();
			const pendingLogin = deferred<AuthSession>();
			api.login.mockReturnValueOnce(pendingLogin.promise);
			const service = new AuthService(api);

			const login = service.login('valid-token');
			await vi.waitFor(() => expect(api.login).toHaveBeenCalledOnce());
			const logout = service.logout();
			pendingLogin.resolve(createSession('stale-token'));
			await Promise.all([login, logout]);

			expect(api.logout).toHaveBeenCalledOnce();
			expect(service.user).toBeNull();
			expect(service.accessToken).toBeNull();
			expect(service.isAuthenticated).toBe(false);
		});

		it('prevents an earlier in-flight refresh from restoring the session', async () => {
			const api = createAuthApi();
			const pendingRefresh = deferred<AuthSession>();
			api.refresh.mockReturnValueOnce(pendingRefresh.promise);
			const service = new AuthService(api);

			const refresh = service.refresh();
			await vi.waitFor(() => expect(api.refresh).toHaveBeenCalledOnce());
			const logout = service.logout();
			pendingRefresh.resolve(createSession('stale-token'));
			await Promise.all([refresh, logout]);

			expect(api.logout).toHaveBeenCalledOnce();
			expect(service.user).toBeNull();
			expect(service.accessToken).toBeNull();
			expect(service.isAuthenticated).toBe(false);
			expect(service.isLoading).toBe(false);
		});
	});
});
