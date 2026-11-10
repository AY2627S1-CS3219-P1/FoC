<script lang="ts">
	import { Card } from 'm3-svelte';
	import { creditAdminApi } from '$lib/services';
	import CardState from './CardState.svelte';
	import { Loadable } from './loadable.svelte';
	import { formatCount } from './format';

	let { refresh }: { refresh: number } = $props();

	const totals = new Loadable(() => creditAdminApi.getCreditTotals(), 'credit totals');

	$effect(() => {
		void refresh;
		void totals.load();
	});
</script>

<Card variant="outlined">
	<div class="panel-body">
		<div class="card-intro">
			<h2>Credits</h2>
			<p class="secondary-text">
				Reserved credits are held now. Returned and paid totals cover every reservation so far.
			</p>
		</div>
		<CardState source={totals} label="credit totals">
			{#snippet children(credit)}
				<dl class="stat-tiles">
					<div><dt>Reserved</dt><dd>{formatCount(credit.reserved)}</dd></div>
					<div><dt>Returned to requesters</dt><dd>{formatCount(credit.returned)}</dd></div>
					<div><dt>Paid to couriers</dt><dd>{formatCount(credit.transferred)}</dd></div>
				</dl>
			{/snippet}
		</CardState>
	</div>
</Card>
