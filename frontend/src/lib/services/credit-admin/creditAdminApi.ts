import type { CalendarDate, DateRange } from '$lib/calendar-date';

/** Credit totals across all users, read from one snapshot. */
export interface CreditTotals {
	/** Credits currently held by open reservations. */
	reserved: bigint;
	/** Credits returned to requesters by every released or settled reservation. */
	returned: bigint;
	/** Credits paid to couriers by every settled reservation. */
	transferred: bigint;
}

/** Payments to couriers settled on one Singapore calendar day. */
export interface DailyTransactionVolume {
	date: CalendarDate;
	/** Credits paid to couriers. */
	creditsTransferred: bigint;
	/** Settlements that paid a courier credits; zero-credit settlements are not counted. */
	paymentCount: bigint;
}

export interface CreditAdminApi {
	getCreditTotals(): Promise<CreditTotals>;
	/** One entry for every day in the range, oldest first, including days with no payments. */
	getDailyTransactionVolume(range: DateRange): Promise<DailyTransactionVolume[]>;
}
