<script lang="ts">
	import type { Presentation } from '$lib/services/review-moderation/presentation';

	let { label, tone }: { label: string; tone: Presentation['tone'] } = $props();
</script>

<span class="status-chip" data-tone={tone}>{label}</span>

<style>
	.status-chip {
		display: inline-block;
		padding: 0.2rem 0.75rem;
		border-radius: 999px;
		font-size: 0.8rem;
		font-weight: 600;
	}
	.status-chip[data-tone='pending'] {
		background: var(--md-sys-color-secondary-container, #e8def8);
		color: var(--md-sys-color-on-secondary-container, #1d192b);
	}
	.status-chip[data-tone='approved'] {
		background: var(--md-sys-color-primary-container, #d7e3ff);
		color: var(--md-sys-color-on-primary-container, #001b3f);
	}
	.status-chip[data-tone='rejected'] {
		background: var(--md-sys-color-error-container, #f9dedc);
		color: var(--md-sys-color-on-error-container, #410e0b);
	}
	.status-chip[data-tone='error'] {
		background: var(--md-sys-color-tertiary-container, #ffd8e4);
		color: var(--md-sys-color-on-tertiary-container, #31111d);
	}
</style>
