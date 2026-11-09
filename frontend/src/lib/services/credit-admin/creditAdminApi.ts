/** Credit totals across all users, read from one snapshot. */
export interface CreditTotals {
	/** Credits currently held by open reservations. */
	reserved: bigint;
	/** Credits returned to requesters by every released or settled reservation. */
	returned: bigint;
	/** Credits paid to couriers by every settled reservation. */
	transferred: bigint;
}

export interface CreditAdminApi {
	getCreditTotals(): Promise<CreditTotals>;
}
