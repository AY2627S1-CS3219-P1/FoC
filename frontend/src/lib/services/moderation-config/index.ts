import { createClient, type Transport } from '@connectrpc/connect';
import { ModerationAdminService } from '$lib/gen/moderation/v1/config_pb';
import { ConnectModerationConfigApi } from './connectModerationConfigApi.ts';
import { ModerationConfigService } from './service.svelte.ts';

// Composition-root factory. It wires the generated admin client through the
// transport, the domain adapter, and the rune-state service. Like other domains
// it returns a fresh service; routes compose singletons from this in
// services/index.ts.
export function createModerationConfigService(transport: Transport): ModerationConfigService {
	const client = createClient(ModerationAdminService, transport);
	return new ModerationConfigService(new ConnectModerationConfigApi(client));
}
