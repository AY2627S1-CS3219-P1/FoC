import { browser } from '$app/environment';
import { goto } from '$app/navigation';
import { Code, ConnectError, type Interceptor } from '@connectrpc/connect';

// Wrap the auth interceptor so refresh and retry finish before redirecting.
export const authRedirectInterceptor: Interceptor = (next) => async (request) => {
	try {
		return await next(request);
	} catch (error) {
		if (browser && error instanceof ConnectError && error.code === Code.Unauthenticated) {
			await goto('/login', { replaceState: true });
		}
		throw error;
	}
};
