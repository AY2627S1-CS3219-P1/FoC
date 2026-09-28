import { createClient } from '@connectrpc/connect';
import {
	AuthService as AuthServiceDefinition,
	type User as ProtoUser
} from '$lib/gen/user/v1/auth_pb';
import { createCookieTransport } from '$lib/api/cookie-transport';
import type { AuthRpc, AuthSession, AuthUser } from './auth-rpc';

function toUser(user: ProtoUser | undefined): AuthUser {
	if (!user) {
		throw new Error('Auth response did not include a user');
	}
	return {
		id: user.id,
		email: user.email,
		displayName: user.displayName,
		role: user.role
	};
}

function toSession(accessToken: string, user: ProtoUser | undefined): AuthSession {
	if (!accessToken) {
		throw new Error('Auth response did not include an access token');
	}
	return { accessToken, user: toUser(user) };
}

export class ConnectAuthRpc implements AuthRpc {
	readonly #client = createClient(AuthServiceDefinition, createCookieTransport());

	async requestLink(email: string): Promise<void> {
		await this.#client.requestLink({ email });
	}

	async login(token: string): Promise<AuthSession> {
		const response = await this.#client.login({ token });
		return toSession(response.accessToken, response.user);
	}

	async register(token: string, displayName: string): Promise<AuthSession> {
		const response = await this.#client.register({ token, displayName });
		return toSession(response.accessToken, response.user);
	}

	async refresh(): Promise<AuthSession> {
		const response = await this.#client.refresh({});
		return toSession(response.accessToken, response.user);
	}

	async logout(): Promise<void> {
		await this.#client.logout({});
	}
}
