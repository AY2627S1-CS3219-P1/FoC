import { env } from '$env/dynamic/public';
import type { Interceptor } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';

const baseUrl = env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081';

export function createCustomTransport(interceptors?: Interceptor[]) {
	return createConnectTransport({
		baseUrl,
		useBinaryFormat: false,
		interceptors,
		fetch: (input, init) => fetch(input, { ...init, credentials: 'include' })
	});
}
