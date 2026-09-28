import { env } from '$env/dynamic/public';
import { Code, ConnectError, createClient, type Interceptor } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { AuthService, type User as ProtoUser } from '$lib/gen/user/v1/auth_pb';
import { auth, type User } from '$lib/state/auth.svelte';

const baseUrl = env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081';

function createCookieTransport(interceptors?: Interceptor[]) {
	return createConnectTransport({
		baseUrl,
		useBinaryFormat: false,
		interceptors,
		fetch: (input, init) => fetch(input, { ...init, credentials: 'include' })
	});
}

const refreshClient = createClient(AuthService, createCookieTransport());
let refreshInFlight: Promise<void> | undefined;

function toUser(user: ProtoUser | undefined): User {
	if (!user) {
		throw new Error('Refresh response did not include a user');
	}
	return {
		id: user.id,
		email: user.email,
		displayName: user.displayName,
		role: user.role
	};
}

function refreshSession(): Promise<void> {
	if (refreshInFlight) {
		return refreshInFlight;
	}

	auth.setLoading(true);
	refreshInFlight = refreshClient
		.refresh({})
		.then((response) => auth.setAccessToken(response.accessToken, toUser(response.user)))
		.catch((error: unknown) => {
			auth.logout();
			throw error;
		})
		.finally(() => {
			auth.setLoading(false);
			refreshInFlight = undefined;
		});

	return refreshInFlight;
}

export async function restoreSession(): Promise<void> {
	try {
		await refreshSession();
	} catch {}
}

export const authInterceptor: Interceptor = (next) => async (req) => {
	if (auth.accessToken) {
		req.header.set('Authorization', `Bearer ${auth.accessToken}`);
	}

	try {
		return await next(req);
	} catch (error) {
		if (
			!(error instanceof ConnectError) ||
			error.code !== Code.Unauthenticated ||
			req.service.typeName === AuthService.typeName
		) {
			throw error;
		}

		try {
			await refreshSession();
		} catch {
			throw error;
		}

		if (req.stream) {
			throw error;
		}

		const token = auth.accessToken;
		if (!token) {
			auth.logout();
			throw error;
		}

		req.header.set('Authorization', `Bearer ${token}`);
		try {
			return await next(req);
		} catch (retryError) {
			if (retryError instanceof ConnectError && retryError.code === Code.Unauthenticated) {
				auth.logout();
			}
			throw retryError;
		}
	}
};

export const transport = createCookieTransport([authInterceptor]);
