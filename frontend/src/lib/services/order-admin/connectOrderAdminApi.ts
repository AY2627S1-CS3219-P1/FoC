import type { Client } from '@connectrpc/connect';
import { OrderAdminService } from '$lib/gen/order/v1/admin_pb';
import type { OrderAdminApi, OrderStatusCount } from './orderAdminApi.ts';

type OrderAdminClient = Client<typeof OrderAdminService>;

export class ConnectOrderAdminApi implements OrderAdminApi {
	readonly #client: OrderAdminClient;

	constructor(client: OrderAdminClient) {
		this.#client = client;
	}

	async getOrderStats(): Promise<OrderStatusCount[]> {
		const { statusCounts } = await this.#client.getOrderStats({});
		return statusCounts.map(({ status, count }) => ({ status, count }));
	}
}
