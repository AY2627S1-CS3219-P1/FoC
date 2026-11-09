<script lang="ts">
	import { Card } from 'm3-svelte';
	import { adminService } from '$lib/services';
	import CardState from './CardState.svelte';
	import { Loadable } from './loadable.svelte';
	import { formatCount } from './format';

	let { refresh }: { refresh: number } = $props();

	const stats = new Loadable(() => adminService.getUserStats(), 'user counts');

	$effect(() => {
		void refresh;
		void stats.load();
	});
</script>

<Card variant="outlined">
	<div class="panel-body">
		<div class="card-intro">
			<h2>Users</h2>
			<p class="secondary-text">Deleted accounts are not counted. Active users include administrators.</p>
		</div>
		<CardState source={stats} label="user counts">
			{#snippet children(counts)}
				<dl class="stat-tiles">
					<div><dt>Active</dt><dd>{formatCount(counts.activeCount)}</dd></div>
					<div><dt>Suspended</dt><dd>{formatCount(counts.suspendedCount)}</dd></div>
				</dl>
			{/snippet}
		</CardState>
	</div>
</Card>
