<script lang="ts">
	import { Button, Card } from 'm3-svelte';
</script>

<svelte:head><title>Administration · Friend on Campus</title></svelte:head>
<section class="page-content admin-overview">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1>Campus administration</h1>
			<p>Maintain locations and review proposals from the campus community.</p>
		</div>
	</header>
	<Card variant="outlined"
		><div class="panel-body">
			<h2>Locations and suppliers</h2>
			<p>Create, edit, archive or restore locations and manage temporary closures.</p>
			<Button href="/admin/suppliers">Manage locations</Button>
		</div></Card
	>
	<Card variant="outlined"
		><div class="panel-body">
			<h2>Addition requests</h2>
			<p>Review proposed suppliers and facilities before publishing them.</p>
			<Button href="/admin/supplier-requests">Review requests</Button>
		</div></Card
	>
</section>

<style>
	.admin-overview {
		width: 100%;
		grid-template-columns: repeat(auto-fit, minmax(min(100%, 20rem), 1fr));
	}

	.page-heading {
		grid-column: 1 / -1;
	}
</style>
