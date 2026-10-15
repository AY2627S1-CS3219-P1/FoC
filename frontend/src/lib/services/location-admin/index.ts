import { createClient, type Transport } from '@connectrpc/connect';
import { LocationAdminService } from '$lib/gen/supplier/location/v1/admin_pb';
import { ConnectLocationAdminApi } from './connectAdminApi';
export function createLocationAdminApi(transport: Transport): ConnectLocationAdminApi {
 return new ConnectLocationAdminApi(createClient(LocationAdminService, transport));
}
