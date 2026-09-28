import { authService } from '$lib/auth/auth-service.svelte';
import { createCookieTransport } from '$lib/api/cookie-transport';

export const transport = createCookieTransport([authService.interceptor]);
