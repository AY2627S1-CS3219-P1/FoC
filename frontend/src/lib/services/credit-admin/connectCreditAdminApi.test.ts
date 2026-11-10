import { describe, expect, it, vi } from 'vitest';
import type { Client } from '@connectrpc/connect';
import { CreditAdminService } from '$lib/gen/credit/v1/admin_pb';
import { ConnectCreditAdminApi } from './connectCreditAdminApi.ts';

describe('ConnectCreditAdminApi', () => {
	it('returns the credit totals', async () => {
		const client = {
			getCreditTotals: vi.fn(async () => ({ reserved: 40n, returned: 15n, transferred: 120n }))
		} as unknown as Client<typeof CreditAdminService>;

		expect(await new ConnectCreditAdminApi(client).getCreditTotals()).toEqual({
			reserved: 40n,
			returned: 15n,
			transferred: 120n
		});
		expect(client.getCreditTotals).toHaveBeenCalledWith({});
	});

	it('sends the date range and returns plain calendar dates', async () => {
		const start = { year: 2026, month: 11, day: 9 };
		const end = { year: 2026, month: 11, day: 10 };
		const client = {
			getDailyTransactionVolume: vi.fn(async () => ({
				days: [
					{
						date: { ...start, $typeName: 'google.type.Date' },
						creditsTransferred: 45n,
						paymentCount: 3n
					},
					{
						date: { ...end, $typeName: 'google.type.Date' },
						creditsTransferred: 0n,
						paymentCount: 0n
					}
				]
			}))
		} as unknown as Client<typeof CreditAdminService>;

		const api = new ConnectCreditAdminApi(client);
		expect(await api.getDailyTransactionVolume({ start, end })).toEqual([
			{ date: start, creditsTransferred: 45n, paymentCount: 3n },
			{ date: end, creditsTransferred: 0n, paymentCount: 0n }
		]);
		expect(client.getDailyTransactionVolume).toHaveBeenCalledWith({ startDate: start, endDate: end });
	});
});
