<script lang="ts">
	import { onMount } from 'svelte';
	import { Button, Card, Icon, LoadingIndicator } from 'm3-svelte';
	import { snackbarService, warningService } from '$lib/services';
	import type { AccountWarning } from '$lib/services/warning/warningApi';
	import iconView from '@ktibow/iconset-material-symbols/visibility';
	import iconCopy from '@ktibow/iconset-material-symbols/content-copy';

	let warnings = $state<AccountWarning[]>([]);
	let page = $state(1);
	let totalPages = $state(0);
	let totalItems = $state(0);
	let loading = $state(true);
	let error = $state('');

	const dateFormat = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium', timeStyle: 'short' });

	// Show only the first and last four characters so the ID stays readable
	// without exposing the full value.
	function formatOrderId(id: string): string {
		if (id.length <= 8) return id;
		return `${id.slice(0, 4)}.....${id.slice(-4)}`;
	}

	async function copyOrderId(requestId: string): Promise<void> {
		try {
			await navigator.clipboard.writeText(requestId);
			snackbarService.show('Order ID copied to clipboard', { severity: 'success' });
		} catch {
			snackbarService.show('We could not copy the order ID. Select it and copy it manually.', { severity: 'error', timeout: 8000 });
		}
	}

	async function loadWarnings(requestedPage = page) {
		loading = true;
		error = '';
		try {
			const result = await warningService.listMyWarnings({ page: requestedPage });
			warnings = result.warnings;
			page = result.page;
			totalPages = result.totalPages;
			totalItems = result.totalItems;
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
				{@const isRemoved = warning.status === 'removed'}
				<!-- Application-owned card rather than m3 Card: Card hardcodes
				     var(--m3c-surface) with no background prop, so tinted
				     active/removed states cannot be themed through it. -->
				<article
					class="warning-card"
					class:is-active={!isRemoved}
					class:is-removed={isRemoved}
					aria-label={`Warning, ${isRemoved ? 'removed' : 'active'}: ${warning.reason}`}
				>
					<header class="warning-top">
						<p class="warning-reason">{warning.reason}</p>
						<span class="view-order">
							<Button
								variant="text"
								iconType="full"
								square
								aria-label={`View order ${warning.requestId}`}
								title={`View order ${warning.requestId}`}
								href={`/orders/${encodeURIComponent(warning.requestId)}`}
							>
								<Icon icon={iconView} size={20} aria-hidden="true" />
							</Button>
						</span>
					</header>
					<dl class="warning-meta">
						<div class="order-id-field">
							<dt>Order ID</dt>
							<dd class="order-id-value">
								<span class="order-id-masked">{formatOrderId(warning.requestId)}</span>
								<Button
									variant="text"
									iconType="full"
									square
									size="xs"
									aria-label="Copy order ID"
									title="Copy order ID"
									onclick={() => void copyOrderId(warning.requestId)}
								>
									<Icon icon={iconCopy} size={18} aria-hidden="true" />
								</Button>
							</dd>
						</div>
						<div><dt>Issued</dt><dd>{dateFormat.format(warning.createdAt)}</dd></div>
						{#if warning.removedAt}<div><dt>Removed</dt><dd>{dateFormat.format(warning.removedAt)}</dd></div>{/if}
						{#if warning.removedReason}<div class="removal-reason"><dt>Removal reason</dt><dd>{warning.removedReason}</dd></div>{/if}
					</dl>
				</article>
			{/each}
		</div>
		{#if totalPages > 1}
			<nav class="pagination" aria-label="Warning pages">
				<Button variant="outlined" disabled={loading || page <= 1} onclick={() => void loadWarnings(page - 1)}>
					Previous
				</Button>
				<span>Page {page} of {totalPages} · {totalItems} warnings</span>
				<Button variant="outlined" disabled={loading || page >= totalPages} onclick={() => void loadWarnings(page + 1)}>
					Next
				</Button>
			</nav>
		{/if}
	{/if}
</section>

<style>
	.warning-list { display: grid; gap: 0.75rem; }

	.warning-card {
		display: grid;
		gap: 0.6rem;
		padding: 0.75rem 1rem;
		border: 1px solid var(--m3c-outline-variant);
		border-radius: var(--m3-shape-medium);
		background: var(--m3c-surface);
	}
	/* Local tokens for the active-state amber; kept beside the snackbar warning
	   palette in app.css, which may align with these in a future theme pass. */
	.warning-card.is-active {
		--warning-container: #fffbeb;
		--warning-outline: #fcd34d;
		border-color: var(--warning-outline);
		background: var(--warning-container);
	}
	.warning-card.is-removed {
		background: var(--m3c-surface-container-low);
	}

	.warning-top {
		display: flex;
		align-items: center;
		gap: 0.4rem;
	}

	.warning-reason {
		flex: 1 1 14rem;
		margin: 0;
		font-weight: 500;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.is-removed .warning-reason { color: var(--m3c-on-surface-variant); }

	.view-order { flex: none; margin-left: auto; }

	.warning-meta {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem 2.5rem;
		margin: 0;
	}
	.warning-meta div { min-width: 7rem; }
	.warning-meta dt { color: var(--m3c-on-surface-variant); font-size: 0.75rem; }
	.warning-meta dd { margin: 0.1rem 0 0; font-size: 0.875rem; overflow-wrap: anywhere; }
	.warning-meta .removal-reason dd { color: var(--m3c-on-surface-variant); }
	.order-id-field { display: grid; gap: 0.15rem; }
	.order-id-value { display: flex; align-items: center; gap: 0.4rem; }
	.order-id-masked {
		font-family: var(--m3-font-mono);
		font-variant-numeric: tabular-nums;
	}

	.pagination { display: flex; align-items: center; justify-content: center; gap: 1rem; margin-top: 1rem; }

	@media (max-width: 30rem) {
		.warning-card { padding: 0.7rem 0.85rem; }
		.view-order { margin-left: 0; }
		.warning-meta { gap: 0.35rem 1.5rem; }
	}
</style>
