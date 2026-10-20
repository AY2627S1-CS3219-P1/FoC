<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Button, Card } from 'm3-svelte';
	import TelegramLinkCard from '$lib/components/telegram/TelegramLinkCard.svelte';
	import { authService } from '$lib/services';

	let linked = $state(false);

	onMount(() => {
		if (!authService.isAuthenticated) void goto('/login', { replaceState: true });
	});
</script>

<svelte:head><title>Connect Telegram · Friend on Campus</title></svelte:head>

<section class="center-stage" aria-labelledby="telegram-title">
	<div class="auth-panel">
		<Card variant="outlined">
			<div class="auth-body">
				<p class="eyebrow">Optional</p>
				<h1 id="telegram-title">Get order updates on Telegram</h1>
				{#if authService.isAuthenticated}
					<TelegramLinkCard onLinked={() => (linked = true)} />
				{/if}
				<p class="hint">You can connect or disconnect anytime from your profile.</p>
				<div class="form-actions">
					<Button variant={linked ? 'filled' : 'text'} onclick={() => void goto('/', { replaceState: true })}>
						{linked ? 'Continue' : 'Skip for now'}
					</Button>
				</div>
			</div>
		</Card>
	</div>
</section>

<style>
	.hint {
		margin: 0;
		color: var(--m3c-on-surface-variant);
		font-size: 0.9rem;
	}
</style>
