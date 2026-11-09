<script lang="ts">
	import { page } from '$app/state';
	import { authService, creditAccountApi } from '$lib/services';

	let available = $state<bigint | null>(null);
	let balanceRequest = 0;

	// Reload after sign-in and on every navigation, so a reservation made on
	// another page shows up. Errors, including a user Credit hasn't seen yet,
	// show nothing rather than a wrong amount.
	$effect(() => {
		const caller = authService.user?.id;
		void page.url.pathname;
		void loadBalance(caller);
	});

	async function loadBalance(caller: string | undefined) {
		const request = ++balanceRequest;
		if (!caller) {
			available = null;
			return;
		}
		try {
			const balance = await creditAccountApi.getAvailableCredits();
			if (request === balanceRequest && authService.user?.id === caller) available = balance;
		} catch {
			if (request === balanceRequest && authService.user?.id === caller) available = null;
		}
	}
</script>

{#if available !== null}
	<span class="credit-balance" aria-live="polite">{available.toString()} credits</span>
{/if}
