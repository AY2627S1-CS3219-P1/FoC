<script lang="ts">
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { LedgerEntryKind, type LedgerEntry } from '$lib/gen/credit/v1/shared_types_pb';
	import { authService, creditAccountApi, creditService, realtimeService } from '$lib/services';

	const account = $derived(creditService.account);
	let entries = $state<LedgerEntry[]>([]);
	let page = $state(1);
	let totalPages = $state(0);
	let error = $state('');
	let loading = $state(true);
	let revision = $state(0);
 let loadedCaller: string | undefined;

	$effect(() => {
		const caller = authService.user?.id;
        if (loadedCaller !== caller) { entries = []; totalPages = 0; page = 1; loadedCaller = caller; }
		const current = page;
		void revision;
		if (!caller) return;
		let active = true;
		loading = true;
		error = '';
		const stop = realtimeService.subscribe(['credits'], () => creditAccountApi.listLedgerEntries(current)
			.then((ledger) => {
				if (!active) return;
				if (current > Math.max(1, ledger.totalPages)) { page = Math.max(1, ledger.totalPages); return; }
				entries = ledger.entries; error = "";
				totalPages = ledger.totalPages;
			})
			.catch(() => active && (error = "Couldn't load your credits. Try again shortly."))
			.finally(() => active && (loading = false)));
		return () => { active = false; stop(); };
	});

	// What each entry meant for the user, in their terms rather than Credit's.
	function label(entry: LedgerEntry): string {
		switch (entry.kind) {
			case LedgerEntryKind.GRANT: return 'Starting credits';
			case LedgerEntryKind.RESERVE: return 'Held for your request';
			case LedgerEntryKind.ADJUST: return 'Hold changed by an accepted offer';
			case LedgerEntryKind.REVERT: return 'Hold restored';
			case LedgerEntryKind.RELEASE: return 'Hold released';
			case LedgerEntryKind.PAYOUT: return entry.balanceDelta > 0n ? 'Earned for a delivery' : 'Paid to your courier';
			case LedgerEntryKind.REFUND: return 'Unused hold returned';
			default: return 'Credit change';
		}
	}

	// Owned credits change only with grants and payouts. Holds move credits between
	// available and held without changing what you own, so they are described instead.
	const signed = (value: bigint) => (value > 0n ? '+' : '−') + (value < 0n ? -value : value).toString();
	function amount(entry: LedgerEntry): { text: string; tone: 'gain' | 'loss' | 'hold' } {
		if (entry.balanceDelta !== 0n) return { text: signed(entry.balanceDelta), tone: entry.balanceDelta > 0n ? 'gain' : 'loss' };
		const held = entry.reservedDelta < 0n ? -entry.reservedDelta : entry.reservedDelta;
		return { text: (entry.reservedDelta > 0n ? 'holds ' : 'frees ') + held.toString(), tone: 'hold' };
	}
	const date = new Intl.DateTimeFormat('en-SG', { day: 'numeric', month: 'short', timeZone: 'Asia/Singapore' });
</script>

<Card variant="outlined">
	<section class="panel-body" aria-labelledby="credits-title">
		<h2 id="credits-title">Credits</h2>
		{#if creditService.failed}<p class="notice error" role="alert">Couldn't refresh your balance.{creditService.account ? ' Showing the last available amount.' : ' Try again shortly.'}</p>{/if}
		{#if error}
			<p class="notice error" role="alert">{error}</p>
			<div><Button variant="outlined" onclick={() => revision++}>Try again</Button></div>
		{/if}
		{#if !account && loading}
			<div class="loading-state"><LoadingIndicator aria-label="Loading credits" /></div>
		{:else if account}
			<div class="totals">
				<div><span class="amount">{account.available.toString()}</span><span class="label">available</span></div>
				{#if account.reserved > 0n}
					<div><span class="amount secondary">{account.reserved.toString()}</span><span class="label">held for your open requests</span></div>
				{/if}
			</div>

			{#if entries.length}
				<h3>History</h3>
				<ul class="ledger">
					{#each entries as entry (entry.id)}
						{@const change = amount(entry)}
						<li>
							<span class="what">
								{#if entry.requestId}<a href={'/orders/' + encodeURIComponent(entry.requestId)}>{label(entry)}</a>
								{:else}{label(entry)}{/if}
								{#if entry.createdAt}<span class="when">{date.format(timestampDate(entry.createdAt))}</span>{/if}
							</span>
							<span class="delta {change.tone}">{change.text}</span>
						</li>
					{/each}
				</ul>
				{#if totalPages > 1}
					<nav class="pagination" aria-label="Credit history pages">
						<Button variant="text" size="s" disabled={loading || page <= 1} onclick={() => page--}>Newer</Button>
						<span>Page {page} of {totalPages}</span>
						<Button variant="text" size="s" disabled={loading || page >= totalPages} onclick={() => page++}>Older</Button>
					</nav>
				{/if}
			{/if}
		{/if}
	</section>
</Card>

<style>
	h2 { margin: 0; font-size: 1.25rem; }
	h3 { margin: 0; font-size: 0.875rem; font-weight: 600; color: var(--m3c-on-surface-variant); }

	.totals { display: flex; flex-wrap: wrap; gap: 2rem; }
	.totals > div { display: grid; }
	.amount { font-size: 2.25rem; font-weight: 750; line-height: 1.1; font-variant-numeric: tabular-nums; }
	.amount.secondary { color: var(--m3c-on-surface-variant); }
	.label { font-size: 0.875rem; color: var(--m3c-on-surface-variant); }

	.ledger { display: grid; margin: 0; padding: 0; list-style: none; }
	.ledger li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.6rem 0;
		border-bottom: 1px solid var(--m3c-outline-variant);
	}
	.ledger li:last-child { border-bottom: 0; }
	.what { display: grid; min-width: 0; }
	.when { font-size: 0.8rem; color: var(--m3c-on-surface-variant); }
	.delta { flex: 0 0 auto; font-weight: 650; font-variant-numeric: tabular-nums; color: var(--m3c-on-surface-variant); }
	.delta.gain { color: var(--m3c-primary); }
	.delta.loss { color: var(--m3c-error); }

	.pagination { display: flex; align-items: center; justify-content: center; gap: 0.75rem; font-size: 0.875rem; }
</style>
