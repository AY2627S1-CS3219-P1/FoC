import { env } from '$env/dynamic/public';
import { createClient } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { HealthService as UserHealthService } from '$lib/gen/user/v1/health_pb';

const userTransport = createConnectTransport({
  baseUrl: env.PUBLIC_USER_SERVICE_URL ?? 'http://localhost:8081',
  useBinaryFormat: false
});

export const userHealthClient = createClient(
  UserHealthService,
  userTransport
);
