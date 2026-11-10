<script lang="ts" generics="T">
	import type { Snippet } from 'svelte';
	import { Button, LoadingIndicator } from 'm3-svelte';
	import type { Loadable } from './loadable.svelte';

	let { source, label, children }: { source: Loadable<T>; label: string; children: Snippet<[T]> } =
		$props();
</script>

{#if source.error}
	<p class="notice error" role="alert">{source.error}</p>
	<div class="form-actions">
		<Button variant="outlined" disabled={source.loading} onclick={() => void source.load()}
			>Try again</Button
		>
	</div>
{:else if source.data === undefined}
	<div class="loading-state" role="status" aria-live="polite">
		<LoadingIndicator aria-label={`Loading ${label}`} />
		<p>Loading {label}…</p>
	</div>
{:else}
	<div aria-busy={source.loading}>{@render children(source.data)}</div>
{/if}
