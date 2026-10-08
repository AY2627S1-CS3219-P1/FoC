import { createClient, type Transport } from '@connectrpc/connect';
import { LocationDiscoveryService } from '$lib/gen/supplier/location/v1/discovery_pb';
import { ConnectLocationApi } from './connectLocationApi.ts';
import type { LocationApi } from './locationApi.ts';

export type * from './locationApi.ts';

export function createLocationApi(transport: Transport): LocationApi {
	return new ConnectLocationApi(createClient(LocationDiscoveryService, transport));
}
