import type { Client } from '@connectrpc/connect';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import { OrderService } from '$lib/gen/order/v1/order_pb';
import type { Order, PageInfo } from '$lib/gen/order/v1/shared_types_pb';
import type { CreateOrderInput, ItemImageGrant, OrderApi, OrderPage, OrderQuery } from './orderApi.ts';

type OrderClient = Client<typeof OrderService>;

function requiredOrder(order: Order | undefined): Order {
	if (!order) throw new Error('Order response did not contain an order');
	return order;
}

function page(orders: Order[], info: PageInfo | undefined): OrderPage {
	if (!info) throw new Error('Order response did not contain pagination');
	return { orders, page: info.page, pageSize: info.pageSize, totalItems: info.totalItems, totalPages: info.totalPages };
}

export class ConnectOrderApi implements OrderApi {
	constructor(private readonly client: OrderClient) {}

	async getOrder(id: string): Promise<Order> {
		return requiredOrder((await this.client.getOrder({ id })).order);
	}

	async listOpenOrders(query: OrderQuery): Promise<OrderPage> {
		const request = {
			page: query.page ?? 1, pageSize: query.pageSize ?? 20,
			pickupLocationId: query.pickupLocationId || undefined,
			deliveryBuildingName: query.deliveryBuildingName ?? ''
		};
		const response = await this.client.listOpenOrders(request);
		return page(response.orders, response.pageInfo);
	}

	async listMyOrders(query: OrderQuery): Promise<OrderPage> {
		const response = await this.client.listMyOrders({
			page: query.page ?? 1, pageSize: query.pageSize ?? 20,
			role: query.role, status: query.status
		});
		return page(response.orders, response.pageInfo);
	}

	async createOrder(input: CreateOrderInput, idempotencyKey: string): Promise<Order> {
		const response = await this.client.createOrder({
			pickupLocationId: input.pickupLocationId,
			delivery: { locationId: input.deliveryLocationId, instructions: input.instructions || undefined },
			itemDescription: input.itemDescription, itemImageIds: input.itemImageIds,
			rewardCredits: BigInt(input.rewardCredits),
			deliveryDuration: { seconds: BigInt(input.deliveryMinutes * 60), nanos: 0 },
			expiresAt: timestampFromDate(new Date(input.expiresAt)), idempotencyKey
		});
		return requiredOrder(response.order);
	}

	async createItemImageUpload(contentType: string, sizeBytes: number): Promise<ItemImageGrant> {
		const response = await this.client.createItemImageUpload({ contentType, sizeBytes: BigInt(sizeBytes) });
		if (!response.imageId || !response.uploadUrl) throw new Error('Incomplete image upload grant');
		return response;
	}
}
