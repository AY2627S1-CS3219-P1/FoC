import { createClient, type Transport } from '@connectrpc/connect';
import { TelegramLinkService } from '$lib/gen/communication/v1/telegram_link_pb';
import { ConnectTelegramLinkApi } from './connectTelegramLinkApi.ts';

export type { TelegramLinkApi, TelegramLinkCode, TelegramLinkStatus } from './telegramLinkApi.ts';
export { pollUntilLinked } from './poll.ts';

export function createTelegramLinkApi(transport: Transport): ConnectTelegramLinkApi {
	return new ConnectTelegramLinkApi(createClient(TelegramLinkService, transport));
}
