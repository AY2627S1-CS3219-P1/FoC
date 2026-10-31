<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, LoadingIndicator } from 'm3-svelte';
	import { telegramLinkApi } from '$lib/services';
	import { pollUntilLinked, type TelegramLinkCode, type TelegramLinkStatus } from '$lib/services/telegram';

	// autoConnect creates the link as soon as the user is known to be unlinked,
	// so the first tap opens Telegram.
	let { onLinked, autoConnect = false }: { onLinked?: (status: TelegramLinkStatus) => void; autoConnect?: boolean } =
		$props();

	type State =
		| { kind: 'loading' }
		| { kind: 'unlinked' }
		| { kind: 'waiting'; link: TelegramLinkCode }
		| { kind: 'linked'; status: TelegramLinkStatus };

	let view = $state<State>({ kind: 'loading' });
	let busy = $state(false);
	let message = $state('');
	let polling: AbortController | null = null;

	function describe(cause: unknown): string {
		if (cause instanceof ConnectError && cause.code === Code.Unavailable) {
			return 'Telegram is unavailable right now. You can connect it later from your profile.';
		}
		return 'Something went wrong. Please try again.';
	}

	function stopPolling() {
		polling?.abort();
		polling = null;
	}

	async function load() {
		view = { kind: 'loading' };
		message = '';
		try {
			const status = await telegramLinkApi.getStatus();
			if (status.linked) view = { kind: 'linked', status };
			else if (autoConnect) await connect();
			else view = { kind: 'unlinked' };
		} catch (cause) {
			view = { kind: 'unlinked' };
			message = describe(cause);
		}
	}

	async function connect() {
		if (busy) return;
		busy = true;
		message = '';
		try {
			const link = await telegramLinkApi.createLink();
			view = { kind: 'waiting', link };
			void waitForLink(link);
		} catch (cause) {
			view = { kind: 'unlinked' };
			message = describe(cause);
		} finally {
			busy = false;
		}
	}

	async function waitForLink(link: TelegramLinkCode) {
		stopPolling();
		const controller = new AbortController();
		polling = controller;
		const status = await pollUntilLinked(telegramLinkApi, { until: link.expiresAt, signal: controller.signal });
		if (controller.signal.aborted) return;
		polling = null;
		if (status) {
			view = { kind: 'linked', status };
			onLinked?.(status);
		} else {
			view = { kind: 'unlinked' };
			message = 'That link expired. Connect again to get a new one.';
		}
	}

	function cancel() {
		stopPolling();
		view = { kind: 'unlinked' };
	}

	async function disconnect() {
		if (busy) return;
		busy = true;
		message = '';
		try {
			await telegramLinkApi.unlink();
			view = { kind: 'unlinked' };
		} catch (cause) {
			message = describe(cause);
		} finally {
			busy = false;
		}
	}

	onMount(() => {
		void load();
	});
	onDestroy(stopPolling);
</script>

<div class="telegram-link">
	{#if message}<p class="notice error" role="alert">{message}</p>{/if}

	{#if view.kind === 'loading'}
		<div class="loading-state" role="status" aria-live="polite">
			<LoadingIndicator aria-label="Checking Telegram" />
			<p>Checking Telegram…</p>
		</div>
	{:else if view.kind === 'linked'}
		<p role="status">
			Connected{view.status.username ? ` as @${view.status.username}` : ''}. Order updates will arrive in Telegram.
		</p>
		<div class="form-actions">
			<Button variant="outlined" disabled={busy} onclick={() => void disconnect()}>
				{busy ? 'Disconnecting…' : 'Disconnect'}
			</Button>
		</div>
	{:else if view.kind === 'waiting'}
		<p role="status" aria-live="polite">
			Open Telegram and press <strong>Start</strong>. This page updates once you're connected.
		</p>
		<div class="form-actions">
			<Button variant="text" onclick={cancel}>Cancel</Button>
			<Button href={view.link.url} target="_blank" rel="noopener noreferrer">Open Telegram</Button>
		</div>
	{:else}
		<p>Get order updates in Telegram. Your Telegram username stays private.</p>
		<div class="form-actions">
			<Button disabled={busy} onclick={() => void connect()}>
				{busy ? 'Preparing…' : 'Connect Telegram'}
			</Button>
		</div>
	{/if}
</div>

<style>
	.telegram-link {
		display: grid;
		gap: 1rem;
	}

	.telegram-link > p {
		margin: 0;
		line-height: 1.5;
	}
</style>
