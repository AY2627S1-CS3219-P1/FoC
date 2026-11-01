import { createClient, type Transport } from '@connectrpc/connect';
import { WarningService as WarningServiceDefinition } from '$lib/gen/user/v1/warning_pb';
import { ConnectWarningApi } from './connectWarningApi.ts';
import { WarningService } from './service.ts';

export function createWarningService(transport: Transport): WarningService {
	return new WarningService(new ConnectWarningApi(createClient(WarningServiceDefinition, transport)));
}
