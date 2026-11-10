import type { CalendarDate, DateRange } from '$lib/calendar-date';
import type { OrderStatus } from '$lib/gen/order/v1/shared_types_pb';

/** The number of orders currently in one status. */
export interface OrderStatusCount {
	status: OrderStatus;
	count: bigint;
}

/** Orders created on one Singapore calendar day. */
export interface DailyOrderCount {
	date: CalendarDate;
	count: bigint;
}

export interface OrderAdminApi {
	/** One entry for every defined status, in enum order, including empty statuses. */
	getOrderStats(): Promise<OrderStatusCount[]>;
	/** One entry for every day in the range, oldest first, including days with no orders. */
	getDailyOrderCounts(range: DateRange): Promise<DailyOrderCount[]>;
}
