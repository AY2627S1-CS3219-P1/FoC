import { describe, expect, it, vi } from 'vitest';
import { timestampFromDate } from '@bufbuild/protobuf/wkt';
import type { Client } from '@connectrpc/connect';
import { TelegramLinkService } from '$lib/gen/communication/v1/telegram_link_pb';
import { ConnectTelegramLinkApi } from './connectTelegramLinkApi.ts';

const expiresAt = new Date('2026-10-14T10:10:00Z');
const linkedAt = new Date('2026-10-14T10:01:00Z');

function api(overrides: Record<string, unknown>) {
	const client = {
		createTelegramLink: vi.fn(),
		getTelegramLink: vi.fn(),
		deleteTelegramLink: vi.fn(async () => ({})),
		...overrides
	} as unknown as Client<typeof TelegramLinkService>;
	return { client, api: new ConnectTelegramLinkApi(client) };
}

describe('ConnectTelegramLinkApi', () => {
	it('returns the link URL and its expiry', async () => {
		const { api: linkApi } = api({
			createTelegramLink: vi.fn(async () => ({
				linkUrl: 'https://t.me/foc_bot?start=abc', expiresAt: timestampFromDate(expiresAt)
			}))
		});
		expect(await linkApi.createLink()).toEqual({ url: 'https://t.me/foc_bot?start=abc', expiresAt });
	});

	it('maps linked and unlinked status', async () => {
		const getTelegramLink = vi.fn()
			.mockResolvedValueOnce({ linked: true, telegramUsername: 'casey', linkedAt: timestampFromDate(linkedAt) })
			.mockResolvedValueOnce({ linked: false, telegramUsername: undefined, linkedAt: undefined });
		const { api: linkApi } = api({ getTelegramLink });

		expect(await linkApi.getStatus()).toEqual({ linked: true, username: 'casey', linkedAt });
		expect(await linkApi.getStatus()).toEqual({ linked: false });
	});

	it('unlinks', async () => {
		const { client, api: linkApi } = api({});
		await linkApi.unlink();
		expect(client.deleteTelegramLink).toHaveBeenCalledWith({});
	});
});
