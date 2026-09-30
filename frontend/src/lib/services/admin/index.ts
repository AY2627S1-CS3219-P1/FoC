import { createClient, type Transport } from '@connectrpc/connect';
import { UserAdminService as UserAdminServiceDefinition } from '$lib/gen/user/v1/admin_pb';
import { ConnectAdminApi } from './connectAdminApi.ts';
import { AdminService } from './service.ts';

export function createAdminService(transport: Transport): AdminService {
	return new AdminService(new ConnectAdminApi(createClient(UserAdminServiceDefinition, transport)));
}
