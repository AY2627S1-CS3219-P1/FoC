import { timestampDate } from '@bufbuild/protobuf/wkt';
import type { Client } from '@connectrpc/connect';
import { TelegramLinkService } from '$lib/gen/communication/v1/telegram_link_pb';
import type { TelegramLinkApi, TelegramLinkCode, TelegramLinkStatus } from './telegramLinkApi.ts';

type TelegramLinkClient = Client<typeof TelegramLinkService>;

export class ConnectTelegramLinkApi implements TelegramLinkApi {
	readonly #client: TelegramLinkClient;

	constructor(client: TelegramLinkClient) {
		this.#client = client;
	}

	async createLink(): Promise<TelegramLinkCode> {
		const response = await this.#client.createTelegramLink({});
		if (!response.expiresAt) throw new Error('Telegram link response did not include an expiry');
		return { url: response.linkUrl, expiresAt: timestampDate(response.expiresAt) };
	}

	async getStatus(): Promise<TelegramLinkStatus> {
		const response = await this.#client.getTelegramLink({});
		if (!response.linked) return { linked: false };
		return {
			linked: true,
			username: response.telegramUsername,
			linkedAt: response.linkedAt ? timestampDate(response.linkedAt) : undefined
		};
	}

	async unlink(): Promise<void> {
		await this.#client.unlinkTelegram({});
	}
}
