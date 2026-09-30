import { env } from '$env/dynamic/public';
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { AuthService as UserAuthService } from '$lib/gen/user/v1/auth_pb';
import { HealthService as UserHealthService } from '$lib/gen/user/v1/health_pb';

const userTransport = createConnectTransport({
  baseUrl: env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081',
  useBinaryFormat: false,
  fetch: (input, init) => fetch(input, { ...init, credentials: 'include' })
});

export const userAuthClient = createClient(
  UserAuthService,
  userTransport
);

export const userHealthClient = createClient(
  UserHealthService,
  userTransport
);
