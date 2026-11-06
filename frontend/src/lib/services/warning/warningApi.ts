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

export interface WarningQuery {
	page?: number;
	pageSize?: number;
}

export interface WarningPage {
	warnings: AccountWarning[];
	page: number;
	pageSize: number;
	totalItems: number;
	totalPages: number;
}

export interface WarningApi {
	listMyWarnings(query?: WarningQuery): Promise<WarningPage>;
}
