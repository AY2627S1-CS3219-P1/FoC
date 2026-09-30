import { env } from '$env/dynamic/public';
import { createCustomTransport } from '$lib/connect/transport';
import { createAuthService } from './auth/index.ts';

const userServiceBaseUrl = env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081';
// Auth RPCs must bypass the auth interceptor so refresh cannot intercept itself.
const plainAuthTransport = createCustomTransport({ baseUrl: userServiceBaseUrl });

export const authService = createAuthService(plainAuthTransport);
