import { createClient } from '@connectrpc/connect';
import { createCustomTransport } from '$lib/connect/transport';
import { AuthService as AuthServiceDefinition } from '$lib/gen/user/v1/auth_pb';
import { AuthService } from './service.ts';
import { ConnectAuthApi } from './connectAuthApi.ts';

const authClient = createClient(AuthServiceDefinition, createCustomTransport());

export const authService = new AuthService(new ConnectAuthApi(authClient));
