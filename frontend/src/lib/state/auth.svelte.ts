import type { UserRole } from '$lib/gen/user/v1/auth_pb';

export interface User {
	id: string;
	email: string;
	displayName: string;
	role: UserRole;
}

export interface UserState {
	readonly user: User | null;
	readonly accessToken: string | null;
	readonly isAuthenticated: boolean;
	readonly isLoading: boolean;
}

export class Auth implements UserState {
	#user = $state<User | null>(null);
	#accessToken = $state<string | null>(null);
	#isLoading = $state(false);

	get user(): User | null {
		return this.#user;
	}

	get accessToken(): string | null {
		return this.#accessToken;
	}

	get isAuthenticated(): boolean {
		return this.#accessToken !== null;
	}

	get isLoading(): boolean {
		return this.#isLoading;
	}

	setAccessToken(token: string, user: User): void {
		this.#accessToken = token;
		this.#user = user;
	}

	logout(): void {
		this.#user = null;
		this.#accessToken = null;
	}

	setLoading(loading: boolean): void {
		this.#isLoading = loading;
	}
}

export const auth = new Auth();
