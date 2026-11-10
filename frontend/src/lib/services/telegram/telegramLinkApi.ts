export interface TelegramLinkCode {
	/** Opens the bot in Telegram; pressing Start there links the account. */
	url: string;
	expiresAt: Date;
}

export interface TelegramLinkStatus {
	linked: boolean;
	username?: string;
	linkedAt?: Date;
	/** The running bot; absent while Telegram is disabled. */
	botUrl?: string;
}

export interface TelegramLinkApi {
	createLink(): Promise<TelegramLinkCode>;
	getStatus(): Promise<TelegramLinkStatus>;
	unlink(): Promise<void>;
}
