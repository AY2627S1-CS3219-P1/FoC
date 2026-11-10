<script lang="ts">
	import { canAttemptPictureRefresh } from './avatar.ts';

	// Refresh only near expiry, so a broken fresh URL cannot loop.
	let {
		url,
		name,
		size = 40,
		expiresAt,
		onerror
	}: {
		url?: string;
		name: string;
		size?: number;
		expiresAt?: number;
		onerror?: () => void;
	} = $props();

	let broken = $state(false);
	let lastRefreshAt = 0;
	$effect(() => {
		// Reset the fallback whenever a fresh URL arrives.
		void url;
		broken = false;
	});

	const initials = $derived.by(() => {
		const words = name.trim().split(/\s+/).filter(Boolean);
		if (!words.length) return '?';
		const first = Array.from(words[0])[0] ?? '';
		const last = words.length > 1 ? (Array.from(words[words.length - 1])[0] ?? '') : '';
		return (first + last).toUpperCase();
	});
</script>

{#if url && !broken}
	<img
		class="avatar"
		src={url}
		alt=""
		aria-hidden="true"
		width={size}
		height={size}
		style={`--avatar-size: ${size}px`}
		loading="lazy"
		referrerpolicy="no-referrer"
		onerror={() => {
			broken = true;
			const now = Date.now();
			if (canAttemptPictureRefresh(expiresAt, lastRefreshAt, now)) {
				lastRefreshAt = now;
				onerror?.();
			}
		}}
	/>
{:else}
	<span
		class="avatar initials"
		style={`--avatar-size: ${size}px`}
		aria-hidden="true">{initials}</span
	>
{/if}

<style>
	.avatar {
		display: inline-grid;
		place-items: center;
		width: var(--avatar-size);
		height: var(--avatar-size);
		flex: 0 0 var(--avatar-size);
		border-radius: 50%;
		overflow: hidden;
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
		object-fit: cover;
		font-size: calc(var(--avatar-size) * 0.38);
		font-weight: 650;
	}
	.initials {
		user-select: none;
	}
</style>
