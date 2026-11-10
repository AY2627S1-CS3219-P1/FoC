<script lang="ts">
	import { caseStatusLabels, type CaseStatus } from '$lib/services/moderation';

	let { status }: { status: CaseStatus } = $props();
</script>

<span class="status" data-status={status}>{caseStatusLabels[status]}</span>

<style>
	.status {
		display: inline-block;
		padding: 0.15rem 0.6rem;
		border-radius: var(--m3-shape-full, 999px);
		font-size: 0.75rem;
		font-weight: 500;
		white-space: nowrap;
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
	}
	.status[data-status='awaiting_response'] {
		background: var(--m3c-tertiary-container);
		color: var(--m3c-on-tertiary-container);
	}
	.status[data-status='closed'] {
		background: var(--m3c-surface-container-high);
		color: var(--m3c-on-surface-variant);
	}
</style>
