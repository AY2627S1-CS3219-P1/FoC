import { createClient, type Transport } from '@connectrpc/connect';
import { LocationDisablementService } from '$lib/gen/supplier/location/v1/disablement_pb';
import { ConnectDisablementApi } from './connectDisablementApi';
export function createDisablementApi(transport: Transport): ConnectDisablementApi {
 return new ConnectDisablementApi(createClient(LocationDisablementService, transport));
}
