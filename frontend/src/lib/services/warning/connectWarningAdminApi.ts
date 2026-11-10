import type { Client } from '@connectrpc/connect';
import { WarningAdminService } from '$lib/gen/user/warning/v1/admin_pb';
import { toPageInfo, toWarning } from './connectWarningApi.ts';
import type { WarningPage, WarningQuery } from './warningApi.ts';

type WarningAdminClient = Client<typeof WarningAdminService>;

/** Administrators read any user's warnings, active and removed. */
export class ConnectWarningAdminApi {
	readonly #client: WarningAdminClient;

	constructor(client: WarningAdminClient) {
		this.#client = client;
	}

	async listWarningsForUser(userId: string, query: WarningQuery = {}): Promise<WarningPage> {
		const response = await this.#client.listWarningsForUser({
			userId,
			pagination: {
				page: BigInt(query.page ?? 1),
				pageSize: BigInt(query.pageSize ?? 20)
			}
		});
		return {
			warnings: response.warnings.map(toWarning),
			...toPageInfo(response.pagination)
		};
	}
}
