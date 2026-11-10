import { createClient, type Transport } from '@connectrpc/connect';
import { WarningService as WarningServiceDefinition } from '$lib/gen/user/warning/v1/warning_pb';
import { ConnectWarningApi } from './connectWarningApi.ts';
import { ConnectWarningAdminApi } from './connectWarningAdminApi.ts';
import { WarningAdminService } from '$lib/gen/user/warning/v1/admin_pb';
import { WarningService } from './service.ts';

export function createWarningService(transport: Transport): WarningService {
	return new WarningService(new ConnectWarningApi(createClient(WarningServiceDefinition, transport)));
}

export function createWarningAdminApi(transport: Transport): ConnectWarningAdminApi {
	return new ConnectWarningAdminApi(createClient(WarningAdminService, transport));
}
