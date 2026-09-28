import { createClient, type Transport } from '@connectrpc/connect';
import { AuthService as AuthServiceDefinition } from '$lib/gen/user/v1/auth_pb';
import { AuthService } from './service.svelte.ts';
import { ConnectAuthApi } from './connectAuthApi.ts';

export function createAuthService(transport: Transport): AuthService {
	const authClient = createClient(AuthServiceDefinition, transport);
	return new AuthService(new ConnectAuthApi(authClient));
}
