import type { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';

/** The number of orders currently in one status. */
export interface OrderStatusCount {
	status: OrderStatus;
	count: bigint;
}

export interface OrderAdminApi {
	/** One entry for every defined status, in enum order, including empty statuses. */
	getOrderStats(): Promise<OrderStatusCount[]>;
}
