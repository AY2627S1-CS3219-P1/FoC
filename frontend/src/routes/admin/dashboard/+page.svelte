<script lang="ts">
	import { Button } from 'm3-svelte';
	import CreditTotalsCard from './CreditTotalsCard.svelte';
	import OrderStatusCard from './OrderStatusCard.svelte';
	import UserCountsCard from './UserCountsCard.svelte';

	// Each card reads its own service, so one unavailable service leaves the others visible.
	let refresh = $state(0);
</script>

<svelte:head><title>Dashboard · Friend on Campus</title></svelte:head>

<section class="page-content admin-content" aria-labelledby="dashboard-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1 id="dashboard-title">Dashboard</h1>
			<p>Current counts across orders, users and credits.</p>
		</div>
		<Button variant="outlined" onclick={() => refresh++}>Refresh</Button>
	</header>

	<div class="dashboard-grid">
		<div class="orders"><OrderStatusCard {refresh} /></div>
		<UserCountsCard {refresh} />
		<CreditTotalsCard {refresh} />
	</div>
</section>

<style>
	.dashboard-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 1rem;
		align-items: start;
	}

	.orders {
		grid-column: 1 / -1;
	}

	@media (max-width: 48rem) {
		.dashboard-grid {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
