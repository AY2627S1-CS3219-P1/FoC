import type { Client } from '@connectrpc/connect';
import { timestampDate } from '@bufbuild/protobuf/wkt';
import {
	WarningService as WarningServiceDefinition,
	WarningStatus as ProtoWarningStatus,
	type AccountWarning as ProtoAccountWarning
} from '$lib/gen/user/v1/warning_pb';
import type { AccountWarning, WarningApi, WarningStatus } from './warningApi.ts';

type WarningClient = Client<typeof WarningServiceDefinition>;

function toWarning(warning: ProtoAccountWarning): AccountWarning {
	if (!warning.createdAt) throw new Error('Warning response did not include a creation time');
	return {
		id: warning.id,
		requestId: warning.requestId,
		reason: warning.reason,
		status: toStatus(warning.status),
		createdAt: timestampDate(warning.createdAt),
		removedAt: warning.removedAt ? timestampDate(warning.removedAt) : null,
		removedReason: warning.removedReason ?? null
	};
}

function toStatus(status: ProtoWarningStatus): WarningStatus {
	switch (status) {
		case ProtoWarningStatus.ACTIVE:
			return 'active';
		case ProtoWarningStatus.REMOVED:
			return 'removed';
		default:
			throw new Error('Warning response included an unknown status');
	}
}

export class ConnectWarningApi implements WarningApi {
	readonly #client: WarningClient;

	constructor(client: WarningClient) {
		this.#client = client;
	}

	async getMyWarnings(): Promise<AccountWarning[]> {
		const response = await this.#client.getMyWarnings({});
		return response.warnings.map(toWarning);
	}
}
