import type { Order, OrderStatus, ParticipantRole } from '$lib/gen/order/v1/shared_types_pb';

export interface OrderQuery {
	page?: number;
	pageSize?: number;
	pickupLocationId?: string;
	deliveryBuildingName?: string;
	role?: ParticipantRole;
	status?: OrderStatus;
}

export interface OrderPage {
	orders: Order[];
	page: number;
	pageSize: number;
	totalItems: bigint;
	totalPages: number;
}

/** JSON-safe submitted values. Keep these unchanged when retrying an uncertain creation. */
export interface CreateOrderInput {
	pickupLocationId: string;
	deliveryLocationId: string;
	instructions: string;
	itemDescription: string;
	rewardCredits: string;
	deliveryMinutes: number;
	expiresAt: string;
	itemImageIds: string[];
}

export interface ItemImageGrant {
	imageId: string;
	uploadUrl: string;
	uploadHeaders: Record<string, string>;
}

/** Foundation operations only. Feature-owned adapters request explicit additions. */
export interface OrderApi {
	getOrder(id: string): Promise<Order>;
	listOpenOrders(query: OrderQuery): Promise<OrderPage>;
	listMyOrders(query: OrderQuery): Promise<OrderPage>;
	createOrder(input: CreateOrderInput, idempotencyKey: string): Promise<Order>;
	createItemImageUpload(contentType: string, sizeBytes: number): Promise<ItemImageGrant>;
}
