import { createClient, type Transport } from '@connectrpc/connect';
import { RealtimeService as RealtimeRpc } from '$lib/gen/communication/v1/realtime_pb';
import { RealtimeService } from './service';

export function createRealtimeService(transport: Transport, refreshAuth: () => Promise<void>) {
 const client = createClient(RealtimeRpc, transport);
 return new RealtimeService({ watch: (signal) => client.watchChanges({}, { signal }) }, refreshAuth);
}
