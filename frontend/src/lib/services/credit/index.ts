import { createClient, type Transport } from '@connectrpc/connect';
import { CreditAccountService } from '$lib/gen/credit/v1/account_pb';
import type { Account, LedgerEntry } from '$lib/gen/credit/v1/shared_types_pb';

export interface LedgerPage {
	entries: LedgerEntry[];
	page: number;
	totalPages: number;
}

/** Advisory account reads. Reservation and settlement stay with Order and Credit. */
export function createCreditAccountApi(transport: Transport) {
	const client = createClient(CreditAccountService, transport);

	async function getAccount(): Promise<Account> {
		const { account } = await client.getMyAccount({});
		if (!account) throw new Error('Credit response did not contain an account');
		return account;
	}

	return {
		getAccount,

		async getAvailableCredits(): Promise<bigint> {
			return (await getAccount()).available;
		},

		/** Newest first. */
		async listLedgerEntries(page = 1, pageSize = 10): Promise<LedgerPage> {
			const { entries, pageInfo } = await client.listMyLedgerEntries({ page, pageSize });
			return { entries, page: pageInfo?.page ?? page, totalPages: pageInfo?.totalPages ?? 0 };
		}
	};
}
