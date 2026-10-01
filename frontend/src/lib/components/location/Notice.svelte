<script lang="ts">
	import type { Snippet } from 'svelte';

	let { tone = 'info', children }: { tone?: 'info' | 'warn' | 'error'; children: Snippet } =
		$props();
</script>

<!-- Uses the shared .notice styles from app.css so it matches the user pages. -->
<p class="notice {tone}" role={tone === 'error' ? 'alert' : 'status'}>{@render children()}</p>

<style>
	.warn {
		background: var(--m3c-tertiary-container);
		color: var(--m3c-on-tertiary-container);
	}
</style>
