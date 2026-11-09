import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { OrderAdminService } from '$lib/gen/order/v1/admin_pb';
import { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';
import { ConnectOrderAdminApi } from './connectOrderAdminApi.ts';

describe('ConnectOrderAdminApi', () => {
	it('returns status counts in response order', async () => {
		const client = {
			getOrderStats: vi.fn(async () => ({
				statusCounts: [
					{ status: OrderStatus.AWAITING_COURIER, count: 4n },
					{ status: OrderStatus.COMPLETED, count: 9n }
				]
			}))
		} as unknown as Client<typeof OrderAdminService>;

		expect(await new ConnectOrderAdminApi(client).getOrderStats()).toEqual([
			{ status: OrderStatus.AWAITING_COURIER, count: 4n },
			{ status: OrderStatus.COMPLETED, count: 9n }
		]);
		expect(client.getOrderStats).toHaveBeenCalledWith({});
	});
});
