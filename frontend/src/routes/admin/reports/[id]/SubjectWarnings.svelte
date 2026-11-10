<script lang="ts">
	import { LoadingIndicator } from 'm3-svelte';
	import { warningAdminApi } from '$lib/services';
	import type { AccountWarning } from '$lib/services/warning/warningApi';

	// The reported user's warning history, for the administrator deciding the
	// report. Counts come from the first page of up to 100 warnings.
	let { userId, name }: { userId: string; name: string } = $props();

	const pageSize = 100;
	const shown = 5;
	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeZone: 'Asia/Singapore' });

	let warnings = $state<AccountWarning[]>([]);
	let total = $state(0);
	let loading = $state(true);
	let failed = $state(false);

	const active = $derived(warnings.filter((warning) => warning.status === 'active').length);
	const removed = $derived(warnings.filter((warning) => warning.status === 'removed').length);
	const partial = $derived(total > warnings.length);

	$effect(() => {
		const id = userId;
		let stale = false;
		loading = true;
		failed = false;
		warningAdminApi
			.listWarningsForUser(id, { pageSize })
			.then((page) => {
				if (stale) return;
				warnings = page.warnings;
				total = page.totalItems;
			})
			.catch(() => {
				if (!stale) failed = true;
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});
</script>

<section class="history" aria-labelledby="subject-warnings-title">
	<h2 id="subject-warnings-title">Warnings for {name || 'the reported user'}</h2>
	{#if loading}
		<div role="status"><LoadingIndicator aria-label="Loading warnings" /></div>
	{:else if failed}
		<p class="notice error" role="alert">The warning history could not be loaded.</p>
	{:else if total === 0}
		<p class="secondary-text">No warnings on record.</p>
	{:else}
		<p class="count">
			<strong>{total} {total === 1 ? 'warning' : 'warnings'}</strong>
			{#if !partial}<span class="secondary-text">({active} active, {removed} removed)</span>{/if}
		</p>
		<ul>
			{#each warnings.slice(0, shown) as warning (warning.id)}
				<li>
					<div class="row-head">
						<span class="reason">{warning.reason}</span>
						<span class="badge" data-status={warning.status}>{warning.status === 'active' ? 'Active' : 'Removed'}</span>
					</div>
					<p class="secondary-text">
						{date.format(warning.createdAt)} ·
						<a href={`/orders/${encodeURIComponent(warning.requestId)}`}>Order</a>
						{#if warning.removedReason}· Removed: {warning.removedReason}{/if}
					</p>
				</li>
			{/each}
		</ul>
		{#if total > shown}<p class="secondary-text">Showing the {shown} most recent.</p>{/if}
	{/if}
</section>

<style>
	.history { display: grid; gap: 0.5rem; }
	h2 { margin: 0; font-size: 1.1rem; }
	p { margin: 0; }
	.count { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: baseline; }
	ul { display: grid; gap: 0.5rem; margin: 0; padding: 0; list-style: none; }
	li {
		display: grid;
		gap: 0.2rem;
		padding: 0.5rem 0.75rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-medium);
	}
	.row-head { display: flex; justify-content: space-between; gap: 0.5rem; }
	.reason { overflow-wrap: anywhere; }
	.badge {
		flex: none;
		padding: 0.1rem 0.55rem;
		border-radius: 999px;
		font-size: 0.75rem;
		background: var(--m3c-error-container);
		color: var(--m3c-on-error-container);
	}
	.badge[data-status='removed'] {
		background: var(--m3c-surface-container-high);
		color: var(--m3c-on-surface-variant);
	}
</style>
