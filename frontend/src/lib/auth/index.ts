import { createClient } from '@connectrpc/connect';
import { createCustomTransport } from '$lib/connect/custom-transport';
import { AuthService as AuthServiceDefinition } from '$lib/gen/user/v1/auth_pb';
import { AuthService } from './auth-service.svelte';
import { ConnectAuthRpc } from './connect-auth-rpc';

const authClient = createClient(AuthServiceDefinition, createCustomTransport());

export const authService = new AuthService(new ConnectAuthRpc(authClient));
