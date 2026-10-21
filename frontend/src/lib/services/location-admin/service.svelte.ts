import { Code, ConnectError, type Interceptor } from '@connectrpc/connect';
import { UserRole } from '$lib/gen/user/v1/auth_pb';
export function isAdministrator(role: UserRole | undefined): boolean {
	return role === UserRole.ADMIN || role === UserRole.SUPER_ADMIN;
}
interface Session {
	readonly user: { id: string; role: UserRole } | null;
	refresh(): Promise<void>;
}
/** Immediately hides administrative data after a denied RPC, even if refresh fails. */
export class SupplierAdminAccess {
	#deniedUserId = $state<string | undefined>();
	get denied(): boolean {
		return this.#deniedUserId !== undefined && this.#deniedUserId === this.session.user?.id;
	}
	constructor(private readonly session: Session) {}
	readonly interceptor: Interceptor = (next) => async (request) => {
		if (this.denied || !isAdministrator(this.session.user?.role)) {
			throw new ConnectError('Administrator access is required.', Code.PermissionDenied);
		}
		const userId = this.session.user?.id;
		try {
			return await next(request);
		} catch (error) {
			const code = ConnectError.from(error).code;
			if (
				this.session.user?.id === userId &&
				(code === Code.PermissionDenied || code === Code.Unauthenticated)
			) {
				this.#deniedUserId = userId;
			}
			throw error;
		}
	};
	async recover() {
		await this.session.refresh();
		this.#deniedUserId = isAdministrator(this.session.user?.role) ? undefined : this.session.user?.id;
	}
}
