import { createClient } from '@connectrpc/connect';
import { HealthService as UserHealthService } from '$lib/gen/user/v1/health_pb';
import { transport } from '$lib/api/transport';

export const userHealthClient = createClient(
  UserHealthService,
  transport
);
