import { env } from '$env/dynamic/public';
import { createCustomTransport } from '$lib/connect/transport';
import { createAuthService } from './auth/index.ts';
import { createLocationApi } from './location/index.ts';

const userServiceBaseUrl = env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081';
// Auth RPCs must bypass the auth interceptor so refresh cannot intercept itself.
const plainAuthTransport = createCustomTransport({ baseUrl: userServiceBaseUrl });

export const authService = createAuthService(plainAuthTransport);

const supplierServiceBaseUrl = env.PUBLIC_SUPPLIER_SERVICE_URL ?? 'http://localhost:8082';
const supplierTransport = createCustomTransport({
	baseUrl: supplierServiceBaseUrl,
	interceptors: [authService.interceptor]
});

export const locationApi = createLocationApi(supplierTransport);
