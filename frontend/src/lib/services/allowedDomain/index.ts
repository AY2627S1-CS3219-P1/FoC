import { createClient, type Transport } from '@connectrpc/connect';
import { AllowedDomainAdminService } from '$lib/gen/user/v1/admin_pb';
import { ConnectAllowedDomainApi } from './connectAllowedDomainApi.ts';
import { AllowedDomainService } from './service.ts';

export function createAllowedDomainService(transport: Transport): AllowedDomainService {
	return new AllowedDomainService(new ConnectAllowedDomainApi(createClient(AllowedDomainAdminService, transport)));
}
