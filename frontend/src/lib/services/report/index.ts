import { createClient, type Transport } from '@connectrpc/connect';
import { ReportService } from '$lib/gen/moderation/report/v1/report_pb';
import { ConnectReportApi } from './connectReportApi.ts';

export type * from './reportApi.ts';

export function createReportApi(transport: Transport): ConnectReportApi {
	return new ConnectReportApi(createClient(ReportService, transport));
}
