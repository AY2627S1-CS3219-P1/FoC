import type { Client } from '@connectrpc/connect';
import type { CalendarDate, DateRange } from '$lib/calendar-date';
import type { Date as ProtoDate } from '$lib/gen/google/type/date_pb';
import { OrderAdminService } from '$lib/gen/order/v1/admin_pb';
import type { DailyOrderCount, OrderAdminApi, OrderStatusCount } from './orderAdminApi.ts';

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

	async getDailyOrderCounts({ start, end }: DateRange): Promise<DailyOrderCount[]> {
		const { days } = await this.#client.getDailyOrderCounts({ startDate: start, endDate: end });
		return days.map(({ date, count }) => ({ date: calendarDate(date), count }));
	}
}

function calendarDate(date: ProtoDate | undefined): CalendarDate {
	if (!date) throw new Error('Order Service returned a daily count without a date');
	return { year: date.year, month: date.month, day: date.day };
}
