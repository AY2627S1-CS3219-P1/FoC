import type { Interceptor } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';

interface CustomTransportOptions {
	baseUrl: string;
	interceptors?: Interceptor[];
}

export function createCustomTransport({ baseUrl, interceptors }: CustomTransportOptions) {
	return createConnectTransport({
		baseUrl,
		useBinaryFormat: false,
		interceptors,
		fetch: (input, init) => fetch(input, { ...init, credentials: 'include' })
	});
}
