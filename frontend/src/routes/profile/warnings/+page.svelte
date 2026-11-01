<script lang="ts">
	import { onMount } from 'svelte';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { warningService } from '$lib/services';
	import type { AccountWarning } from '$lib/services/warning/warningApi';

	let warnings = $state<AccountWarning[]>([]);
	let loading = $state(true);
	let error = $state('');

	const dateFormat = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeStyle: 'short' });

	async function loadWarnings() {
		loading = true;
		error = '';
		try {
			warnings = await warningService.getMyWarnings();
		} catch {
			error = 'We could not load your warnings. Check your connection and try again.';
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void loadWarnings();
	});
</script>

<svelte:head><title>Your warnings · Friend on Campus</title></svelte:head>

<section class="page-content" aria-labelledby="warnings-title">
	<div><Button variant="text" href="/profile">← Your profile</Button></div>
	<header class="page-heading">
		<div><p class="eyebrow">Your account</p><h1 id="warnings-title">Your warnings</h1></div>
	</header>

	{#if loading}
		<div class="loading-state" role="status" aria-live="polite"><LoadingIndicator aria-label="Loading warnings" /><p>Loading your warnings…</p></div>
	{:else if error}
		<Card variant="outlined"><div class="panel-body"><p class="notice error" role="alert">{error}</p><div class="form-actions"><Button onclick={() => void loadWarnings()}>Try again</Button></div></div></Card>
	{:else if warnings.length === 0}
		<Card variant="outlined"><div class="panel-body"><p>You have no warnings.</p></div></Card>
	{:else}
		<div class="warning-list">
			{#each warnings as warning (warning.id)}
				<Card variant="outlined">
					<article class="panel-body warning-item" aria-labelledby={`warning-${warning.id}`}>
						<div class="warning-heading">
							<h2 id={`warning-${warning.id}`}>Warning</h2>
							<span class:removed={warning.status === 'removed'} class="role-chip">{warning.status === 'removed' ? 'Removed' : 'Active'}</span>
						</div>
						<p class="warning-reason">{warning.reason}</p>
						<dl class="account-facts">
							<div><dt>Request ID</dt><dd>{warning.requestId}</dd></div>
							<div><dt>Issued</dt><dd>{dateFormat.format(warning.createdAt)}</dd></div>
							{#if warning.removedAt}<div><dt>Removed</dt><dd>{dateFormat.format(warning.removedAt)}</dd></div>{/if}
							{#if warning.removedReason}<div><dt>Removal reason</dt><dd>{warning.removedReason}</dd></div>{/if}
						</dl>
					</article>
				</Card>
			{/each}
		</div>
	{/if}
</section>

<style>
	.warning-list { display: grid; gap: 1rem; }
	.warning-item h2 { margin: 0; font-size: 1.1rem; }
	.warning-heading { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
	.warning-reason { margin: 1rem 0; white-space: pre-wrap; }
	.account-facts dt { color: var(--m3c-on-surface-variant); font-size: 0.875rem; }
	.account-facts dd { margin: 0.25rem 0 0; overflow-wrap: anywhere; }
	.role-chip.removed { opacity: 0.75; }
</style>
