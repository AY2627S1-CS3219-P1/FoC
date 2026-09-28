import type { Client } from '@connectrpc/connect';
import {
	AuthService as AuthServiceDefinition,
	type User as ProtoUser
} from '$lib/gen/user/v1/auth_pb';
import type { AuthApi, AuthSession, AuthUser } from './auth-api';

type AuthClient = Client<typeof AuthServiceDefinition>;

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

export class ConnectAuthApi implements AuthApi {
	readonly #client: AuthClient;

	constructor(client: AuthClient) {
		this.#client = client;
	}

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
