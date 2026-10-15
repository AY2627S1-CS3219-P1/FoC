<script lang="ts">
	import { fade } from 'svelte/transition';
	import { Icon, SnackbarItem } from 'm3-svelte';
	import iconCheck from '@ktibow/iconset-material-symbols/check-circle';
	import iconError from '@ktibow/iconset-material-symbols/cancel';
	import iconInfo from '@ktibow/iconset-material-symbols/info';
	import iconWarning from '@ktibow/iconset-material-symbols/warning';
	import iconClose from '@ktibow/iconset-material-symbols/close';
	import { snackbarService } from '$lib/services';

	const notice = snackbarService.notice;
	const icons = { info: iconInfo, success: iconCheck, error: iconError, warning: iconWarning };
</script>

{#if $notice}
	<div
		class="holder"
		role={$notice.severity === 'error' ? 'alert' : 'status'}
		aria-live={$notice.severity === 'error' ? 'assertive' : 'polite'}
		aria-atomic="true"
		out:fade={{ duration: 200 }}
	>
		{#key $notice.id}
			<SnackbarItem data-severity={$notice.severity}>
				<Icon class="status-icon" icon={icons[$notice.severity]} size={24} aria-hidden="true" />
				<p>{$notice.message}</p>
				{#each Object.entries($notice.actions) as [label, action]}
					<button
						type="button"
						class="action"
						onclick={() => {
							snackbarService.dismiss();
							action();
						}}
					>
						{label}
					</button>
				{/each}
				<button
					type="button"
					class="close m3-layer"
					title="Dismiss notification"
					aria-label="Dismiss notification"
					onclick={() => snackbarService.dismiss()}
				>
					<Icon icon={iconClose} size={24} aria-hidden="true" />
				</button>
			</SnackbarItem>
		{/key}
	</div>
{/if}
