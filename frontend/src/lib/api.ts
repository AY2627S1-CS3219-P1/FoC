import { createClient } from '@connectrpc/connect';
import { authService } from '$lib/auth';
import { createCustomTransport } from '$lib/connect/custom-transport';
import { HealthService as UserHealthService } from '$lib/gen/user/v1/health_pb';

export const transport = createCustomTransport([authService.interceptor]);

export const userHealthClient = createClient(UserHealthService, transport);
