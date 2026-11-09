import { env } from '$env/dynamic/public';
import { createCustomTransport } from '$lib/connect/transport';
import { authRedirectInterceptor } from '$lib/connect/authRedirect';
import { createAuthService } from './auth/index.ts';
import { createProfileService } from './profile/index.ts';
import { createAdminService } from './admin/index.ts';
import { createAllowedDomainService } from './allowedDomain/index.ts';
import { createLocationApi } from './location/index.ts';
import { createLocationAdminApi } from './location-admin';
import { createAdditionRequestApi } from './addition-request';
import { createDisablementApi } from './disablement';
import { SupplierAdminAccess } from './location-admin/service.svelte';
import { createOrderApi } from './order/index.ts';
import { createDeliveryProofService } from './delivery-proof/index.ts';
import { createParticipantActionsApi } from './order-cancellation/index.ts';
import { createOrderOffersApi } from './order-offers/index.ts';
import { createCreditAccountApi } from './credit/index.ts';
import { createTelegramLinkApi } from './telegram/index.ts';
import { SnackbarService } from './snackbar/service.ts';

export const snackbarService = new SnackbarService();

const userServiceBaseUrl = env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081';
// Auth RPCs must bypass the auth interceptor so refresh cannot intercept itself.
const plainAuthTransport = createCustomTransport({ baseUrl: userServiceBaseUrl });

export const authService = createAuthService(plainAuthTransport);
const protectedUserTransport = createCustomTransport({
	baseUrl: userServiceBaseUrl,
	interceptors: [authRedirectInterceptor, authService.interceptor]
});

export const profileService = createProfileService(protectedUserTransport, authService);
export const adminService = createAdminService(protectedUserTransport);
export const allowedDomainService = createAllowedDomainService(protectedUserTransport);

const supplierServiceBaseUrl = env.PUBLIC_SUPPLIER_SERVICE_URL ?? 'http://localhost:8082';
const supplierTransport = createCustomTransport({
	baseUrl: supplierServiceBaseUrl,
	interceptors: [authRedirectInterceptor, authService.interceptor]
});

export const locationApi = createLocationApi(supplierTransport);

// Administrative capabilities share auth and immediately revoke their UI on denied access.
export const supplierAdminAccess = new SupplierAdminAccess(authService);
const supplierAdminTransport = createCustomTransport({
	baseUrl: supplierServiceBaseUrl,
	interceptors: [supplierAdminAccess.interceptor, authService.interceptor]
});
export const locationAdminApi = createLocationAdminApi(supplierAdminTransport);
export const additionRequestApi = createAdditionRequestApi(supplierAdminTransport);
export const disablementApi = createDisablementApi(supplierAdminTransport);

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

export const participantActionsApi = createParticipantActionsApi(orderTransport);

export const deliveryProofApi = createDeliveryProofService(orderTransport);
const communicationServiceBaseUrl = env.PUBLIC_COMMUNICATION_SERVICE_URL ?? 'http://localhost:8084';
const communicationTransport = createCustomTransport({
	baseUrl: communicationServiceBaseUrl,
	interceptors: [authService.interceptor]
});

export const telegramLinkApi = createTelegramLinkApi(communicationTransport);
