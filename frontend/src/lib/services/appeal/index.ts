import { createClient, type Transport } from '@connectrpc/connect';
import { AppealService } from '$lib/gen/moderation/appeal/v1/appeal_pb';
import { ConnectAppealApi } from './connectAppealApi.ts';

export type * from './appealApi.ts';

export function createAppealApi(transport: Transport): ConnectAppealApi {
	return new ConnectAppealApi(createClient(AppealService, transport));
}
