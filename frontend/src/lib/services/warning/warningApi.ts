export type WarningStatus = 'active' | 'removed';

export interface AccountWarning {
	id: string;
	requestId: string;
	reason: string;
	status: WarningStatus;
	createdAt: Date;
	removedAt: Date | null;
	removedReason: string | null;
}

export interface WarningApi {
	getMyWarnings(): Promise<AccountWarning[]>;
}
