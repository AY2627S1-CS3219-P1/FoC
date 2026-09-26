<script lang="ts">
	import { userHealthClient } from '$lib/api/connect';

	let requestState = $state<'idle' | 'loading' | 'success' | 'error'>('idle');
	let message = $state('Not checked yet.');

	async function checkHealth() {
		requestState = 'loading';
		message = 'Checking user-service...';

		try {
			const response = await userHealthClient.check({});
			requestState = 'success';
			message = `Connect response: ${response.status}`;
		} catch (error) {
			requestState = 'error';
			message = error instanceof Error ? error.message : 'Unknown Connect error';
		}
	}
</script>

<!-- Temporary verification page. Remove once product UI or permanent browser tests cover user Connect. -->
<svelte:head>
	<title>User Connect RPC demo (TO BE REMOVED)</title>
</svelte:head>

<section class="page-content">
	<h1>User Connect RPC</h1>
	<p>This page calls the generated user HealthService client.</p>

	<button onclick={checkHealth} disabled={requestState === 'loading'}>
		{requestState === 'loading' ? 'Checking...' : 'Check user service'}
	</button>

	<p aria-live="polite" class:error={requestState === 'error'}>
		{message}
	</p>
</section>

<style>
	button {
		margin-top: 1rem;
		padding: 0.75rem 1.1rem;
		border: 0;
		border-radius: 0.5rem;
		background: #174a99;
		color: white;
		cursor: pointer;
		font: inherit;
		font-weight: 650;
	}

	button:disabled {
		cursor: wait;
		opacity: 0.65;
	}

	.error {
		color: #a11a1a;
	}
</style>
