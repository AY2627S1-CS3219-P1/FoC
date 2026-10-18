import { create } from '@bufbuild/protobuf';
import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import { OrderSchema, OrderStatus, PageInfoSchema } from '$lib/gen/order/v1/shared_types_pb';
import { ConnectOrderApi } from './connectOrderApi.ts';

// Original Order proto: stable idempotency key/image IDs, int64 credits, seconds.
it('adapts creation without rounding int64 credits or changing retry identity', async () => {
	const order = create(OrderSchema, { id: 'order', status: OrderStatus.AWAITING_COURIER });
	const createOrder = vi.fn(async () => ({ order }));
	const api = new ConnectOrderApi({ createOrder } as unknown as Client<typeof OrderService>);
	const input = { pickupLocationId: 'pickup', deliveryLocationId: 'delivery', itemDescription: 'Meal', instructions: 'Desk', rewardCredits: '9007199254740993', deliveryMinutes: 30, expiresAt: '2026-10-18T11:00:00Z', itemImageIds: ['image'] };
	await api.createOrder(input, 'same-key');
	await api.createOrder(input, 'same-key');
	expect(createOrder.mock.calls[0]).toEqual(createOrder.mock.calls[1]);
	expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
		idempotencyKey: 'same-key', rewardCredits: 9007199254740993n,
		deliveryDuration: { seconds: 1800n, nanos: 0 }, itemImageIds: ['image'],
		delivery: { locationId: 'delivery', instructions: 'Desk' }
	}));
});

it('preserves backend pagination and current Order status', async () => {
	const order = create(OrderSchema, { id: 'order', status: OrderStatus.EXPIRED });
	const api = new ConnectOrderApi({
		getOrder: vi.fn(async () => ({ order })),
		listMyOrders: vi.fn(async () => ({ orders: [order], pageInfo: create(PageInfoSchema, { page: 2, pageSize: 20, totalItems: 21n, totalPages: 2 }) }))
	} as unknown as Client<typeof OrderService>);
	expect((await api.getOrder('order')).status).toBe(OrderStatus.EXPIRED);
	expect(await api.listMyOrders({ page: 2 })).toMatchObject({ page: 2, totalItems: 21n, totalPages: 2 });
});

it('does not replace malformed responses with fabricated success data', async () => {
	const api = new ConnectOrderApi({ getOrder: async () => ({}) } as unknown as Client<typeof OrderService>);
	await expect(api.getOrder('order')).rejects.toThrow('did not contain an order');
});

// The approved privacy contract permits coarse building filtering, not exact IDs.
it('sends the coarse destination contract without an exact destination ID', async () => {
 const listOpenOrders = vi.fn(async (_request: unknown) => ({ orders: [], pageInfo: create(PageInfoSchema) }));
 const api = new ConnectOrderApi({ listOpenOrders } as unknown as Client<typeof OrderService>);
 await api.listOpenOrders({ deliveryBuildingName: 'COM3' });
 expect(listOpenOrders).toHaveBeenCalledWith(expect.objectContaining({ deliveryBuildingName: 'COM3' }));
 expect(listOpenOrders.mock.calls[0]?.[0]).not.toHaveProperty('deliveryLocationId');
});
