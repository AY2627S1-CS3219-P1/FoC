import { browser } from '$app/environment';
import { goto } from '$app/navigation';
import { Code, ConnectError, type Interceptor } from '@connectrpc/connect';

// Wrap the auth interceptor so refresh and retry finish before redirecting.
export function createAuthRedirectInterceptor(
	shouldRedirect: (methodName: string) => boolean = () => true
): Interceptor {
	return (next) => async (request) => {
		try {
			return await next(request);
		} catch (error) {
			if (
				browser &&
				error instanceof ConnectError &&
				error.code === Code.Unauthenticated &&
				shouldRedirect(request.method.name)
			) {
				await goto('/login', { replaceState: true });
			}
			throw error;
		}
	};
}

export const authRedirectInterceptor = createAuthRedirectInterceptor();
export const adminConfigRefreshRedirectInterceptor = createAuthRedirectInterceptor(
	(methodName) => methodName.toLowerCase() === 'refresh' && window.location.pathname === '/admin/config'
);
