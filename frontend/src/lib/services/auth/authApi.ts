import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export interface AuthUser {
	id: string;
	email: string;
	displayName: string;
	role: UserRole;
}

export interface AuthSession {
	accessToken: string;
	user: AuthUser;
}

/** Operations the auth service needs from its backend API. */
export interface AuthApi {
	requestLink(email: string): Promise<void>;
	login(token: string): Promise<AuthSession>;
	register(token: string, displayName: string): Promise<AuthSession>;
	refresh(): Promise<AuthSession>;
	logout(): Promise<void>;
}
