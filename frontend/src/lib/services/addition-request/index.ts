import { createClient, type Transport } from '@connectrpc/connect';
import { LocationAdditionRequestService } from '$lib/gen/supplier/location/v1/addition_request_pb';
import { ConnectAdditionRequestApi } from './connectRequestApi';
export function createAdditionRequestApi(transport: Transport): ConnectAdditionRequestApi {
	return new ConnectAdditionRequestApi(createClient(LocationAdditionRequestService, transport));
}
