import { env } from '$env/dynamic/public';
import { createCustomTransport } from '$lib/connect/transport';
import { createAuthService } from './auth/index.ts';
import { createProfileService } from './profile/index.ts';
import { createAdminService } from './admin/index.ts';
import { createLocationApi } from './location/index.ts';
import { createOrderApi } from './order/index.ts';
import { createOrderOffersApi } from './order-offers/index.ts';
import { createCreditAccountApi } from './credit/index.ts';
import { createTelegramLinkApi } from './telegram/index.ts';

const userServiceBaseUrl = env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081';
// Auth RPCs must bypass the auth interceptor so refresh cannot intercept itself.
const plainAuthTransport = createCustomTransport({ baseUrl: userServiceBaseUrl });

export const authService = createAuthService(plainAuthTransport);
const protectedUserTransport = createCustomTransport({
	baseUrl: userServiceBaseUrl,
	interceptors: [authService.interceptor]
});

export const profileService = createProfileService(protectedUserTransport, authService);
export const adminService = createAdminService(protectedUserTransport);

const supplierServiceBaseUrl = env.PUBLIC_SUPPLIER_SERVICE_URL ?? 'http://localhost:8082';
const supplierTransport = createCustomTransport({
	baseUrl: supplierServiceBaseUrl,
	interceptors: [authService.interceptor]
});

export const locationApi = createLocationApi(supplierTransport);

const orderTransport = createCustomTransport({
	baseUrl: env.PUBLIC_ORDER_SERVICE_URL ?? 'http://localhost:8083',
	interceptors: [authService.interceptor]
});
export const orderApi = createOrderApi(orderTransport);
export const orderOffersApi = createOrderOffersApi(orderTransport);

const creditTransport = createCustomTransport({
	baseUrl: env.PUBLIC_CREDIT_SERVICE_URL ?? 'http://localhost:8085',
	interceptors: [authService.interceptor]
});
export const creditAccountApi = createCreditAccountApi(creditTransport);
const communicationServiceBaseUrl = env.PUBLIC_COMMUNICATION_SERVICE_URL ?? 'http://localhost:8084';
const communicationTransport = createCustomTransport({
	baseUrl: communicationServiceBaseUrl,
	interceptors: [authService.interceptor]
});

export const telegramLinkApi = createTelegramLinkApi(communicationTransport);
