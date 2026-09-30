<script lang="ts">
	import type { Snippet } from 'svelte';

	let { tone = 'info', children }: { tone?: 'info' | 'warn' | 'error'; children: Snippet } =
		$props();
</script>

<p class="notice {tone}">{@render children()}</p>

<style>
	.notice {
		padding: 1rem;
		border: 1px solid #dce3ee;
		border-radius: 0.6rem;
		background: #fff;
	}

	.warn {
		border-color: #f0c77a;
		background: #fdf6e7;
		color: #8a4b00;
	}

	.error {
		border-color: #e3a1a1;
		background: #fdf1f1;
	}
</style>
