import type { Client } from '@connectrpc/connect';
import type { CalendarDate, DateRange } from '$lib/calendar-date';
import { CreditAdminService } from '$lib/gen/credit/v1/admin_pb';
import type { Date as ProtoDate } from '$lib/gen/google/type/date_pb';
import type { CreditAdminApi, CreditTotals, DailyTransactionVolume } from './creditAdminApi.ts';

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

	async getDailyTransactionVolume({ start, end }: DateRange): Promise<DailyTransactionVolume[]> {
		const { days } = await this.#client.getDailyTransactionVolume({
			startDate: start,
			endDate: end
		});
		return days.map(({ date, creditsTransferred, paymentCount }) => ({
			date: calendarDate(date),
			creditsTransferred,
			paymentCount
		}));
	}
}

function calendarDate(date: ProtoDate | undefined): CalendarDate {
	if (!date) throw new Error('Credit Service returned a daily volume without a date');
	return { year: date.year, month: date.month, day: date.day };
}
