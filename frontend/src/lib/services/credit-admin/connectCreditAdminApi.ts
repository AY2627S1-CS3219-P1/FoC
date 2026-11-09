import type { Client } from '@connectrpc/connect';
import { CreditAdminService } from '$lib/gen/credit/v1/admin_pb';
import type { CreditAdminApi, CreditTotals } from './creditAdminApi.ts';

type CreditAdminClient = Client<typeof CreditAdminService>;

export class ConnectCreditAdminApi implements CreditAdminApi {
	readonly #client: CreditAdminClient;

	constructor(client: CreditAdminClient) {
		this.#client = client;
	}

	async getCreditTotals(): Promise<CreditTotals> {
		const { reserved, returned, transferred } = await this.#client.getCreditTotals({});
		return { reserved, returned, transferred };
	}
}
