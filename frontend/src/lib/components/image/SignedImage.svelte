<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { ImageGrant, RefreshImageGrant } from './types';

	let {
		objectId, initialGrant, refresh, sessionKey, alt,
		loading = 'lazy', fit = 'cover', fallback
	}: {
		objectId: string;
		initialGrant?: ImageGrant;
		refresh: RefreshImageGrant;
		/** Stable caller identity/role; access-token rotation must not reset retries. */
		sessionKey?: string;
		alt: string;
		loading?: 'lazy' | 'eager';
		fit?: 'cover' | 'contain';
		fallback?: Snippet;
	} = $props();

	// Each effect owns its request, grant and single retry budget. Neither an old
	// image event nor a late RPC may mutate a newer image/session.
	let attempt = $state<{ url?: string; failed: boolean; recover: () => void }>();
	$effect(() => {
		const id = objectId;
		const initial = initialGrant;
		const callback = refresh;
		void sessionKey;
		const controller = new AbortController();
		let used = false;
		const current = { url: initial?.url, failed: false, recover };
		attempt = current;

		function recover() {
			if (controller.signal.aborted) return;
			if (used) {
				attempt = { ...current, failed: true };
				return;
			}
			used = true;
			// Clear src while waiting, so repeated events from the old img cannot
			// consume the retry intended for the replacement image.
			attempt = { ...current, url: undefined };
			void Promise.resolve().then(() => callback(id, controller.signal)).then((grant) => {
				if (controller.signal.aborted) return;
				if (!grant.url) throw new Error('Missing image URL');
				current.url = grant.url;
				attempt = { ...current };
			}).catch(() => {
				if (!controller.signal.aborted) attempt = { ...current, failed: true };
			});
		}

		if (!initial?.url) recover();
		return () => controller.abort();
	});
</script>

{#if attempt?.failed}
	<span class="fallback" role="img" aria-label={`${alt} is unavailable`}>
		{#if fallback}{@render fallback()}{:else}Photo unavailable{/if}
	</span>
{:else if attempt?.url}
	{#key attempt}
		{@const imageAttempt = attempt}
		<img
			src={attempt.url}
			{alt}
			{loading}
			referrerpolicy="no-referrer"
			onerror={() => { if (attempt === imageAttempt) imageAttempt.recover(); }}
			style:object-fit={fit}
		/>
	{/key}
{:else}
	<span class="fallback" role="status" aria-label="Loading photo"></span>
{/if}

<style>
	img { width: 100%; height: 100%; display: block; }
	.fallback { display: grid; place-items: center; width: 100%; height: 100%; color: var(--m3c-on-surface-variant); }
</style>
