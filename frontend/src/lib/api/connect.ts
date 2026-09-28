import { createClient } from '@connectrpc/connect';
import { AuthService as UserAuthService } from '$lib/gen/user/v1/auth_pb';
import { HealthService as UserHealthService } from '$lib/gen/user/v1/health_pb';
import { transport } from '$lib/api/transport';

export const userAuthClient = createClient(
  UserAuthService,
  transport
);

export const userHealthClient = createClient(
  UserHealthService,
  transport
);
