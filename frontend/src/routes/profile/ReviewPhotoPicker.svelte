<script lang="ts">
	import { SvelteMap } from 'svelte/reactivity';
	import { Button, Icon } from 'm3-svelte';
	import addPhotoIcon from '@ktibow/iconset-material-symbols/add-photo-alternate-outline-rounded';
	import cameraIcon from '@ktibow/iconset-material-symbols/photo-camera';
	import type {
		ReviewPhotosController,
		ReviewPhotosSnapshot
	} from '$lib/features/review-photos/controller';

	// Presentational picker for the staged review photos. The dialog owns the
	// controller so it can attach the ordered photo ids to the submission.
	let { controller, view, disabled = false }: {
		controller: ReviewPhotosController | undefined;
		view: ReviewPhotosSnapshot;
		disabled?: boolean;
	} = $props();
	let fileInput: HTMLInputElement | undefined = $state();
	let cameraInput: HTMLInputElement | undefined = $state();

	// Previews are browser object URLs; they must be revoked when a row or the
	// picker itself goes away.
	let previews = $state(new SvelteMap<string, string>());
	$effect(() => {
		const ids = new Set(view.files.map((row) => row.id));
		for (const [id, url] of previews) {
			if (!ids.has(id)) {
				URL.revokeObjectURL(url);
				previews.delete(id);
			}
		}
		for (const row of view.files) {
			if (!previews.has(row.id)) previews.set(row.id, URL.createObjectURL(row.file));
		}
	});
	$effect(() => () => {
		for (const url of previews.values()) URL.revokeObjectURL(url);
	});

	const busy = $derived(view.busy || disabled);
	const pending = $derived(view.files.some((row) => row.outcome === 'Pending'));
	const canUpload = $derived(
		!busy && !pending && view.files.some((row) => row.outcome === 'Not Submitted' || row.outcome === 'Failed')
	);
	const outcomeLabel = (row: ReviewPhotosSnapshot['files'][number]): string => {
		if (row.message) return '';
		if (row.outcome === 'Succeeded') return 'Uploaded';
		if (row.outcome === 'Pending') return 'Uploading…';
		return 'Ready to upload';
	};
</script>

<div class="photo-selection">
	<div class="selection-heading">
		<h3>Photos</h3>
		<span>{view.files.length} / 5</span>
	</div>
	<p id="review-photo-help" class="file-help">
		Optional. Up to 5 photos. JPEG, PNG, WebP, HEIC or HEIF, up to 10 MB each.
	</p>
	<div class="file-picker">
		<input
			bind:this={fileInput}
			id="review-photo-files"
			aria-describedby="review-photo-help"
			class="file-input"
			type="file"
			accept="image/jpeg,image/png,image/webp,image/heic,image/heif"
			multiple
			disabled={busy}
			onchange={(event) => {
				controller?.addFiles(Array.from(event.currentTarget.files ?? []));
				event.currentTarget.value = '';
			}}
		/>
		<label for="review-photo-files" class="choose-photos" class:disabled={busy}>
			<Icon icon={addPhotoIcon} size={20} aria-hidden="true" /> Add photos
		</label>
		<input
			bind:this={cameraInput}
			id="review-photo-camera"
			type="file"
			accept="image/*"
			capture="environment"
			hidden
			disabled={busy}
			onchange={(event) => {
				const photo = event.currentTarget.files?.[0];
				if (photo) controller?.addFiles([photo]);
				event.currentTarget.value = '';
			}}
		/>
		<span class="camera-action">
			<Button type="button" variant="outlined" iconType="full" aria-label="Take photo" title="Take photo" disabled={busy} onclick={() => cameraInput?.click()}>
				<Icon icon={cameraIcon} size={24} aria-hidden="true" />
			</Button>
		</span>
	</div>
	{#if view.files.length > 0}
		<ul aria-label="Photos attached to this review">
			{#each view.files as row, index (row.id)}
				<li>
					{#if previews.get(row.id)}
						<span class="photo-preview">
							<img src={previews.get(row.id)} alt="" />
						</span>
					{:else}
						<span class="photo-preview placeholder" aria-hidden="true"></span>
					{/if}
					<div class="file-summary">
						<span class="file-outcome">{outcomeLabel(row)}</span>
					</div>
					<div class="remove-action">
						<Button
							type="button"
							variant="text"
							aria-label={`Remove photo ${index + 1}`}
							disabled={busy || row.outcome === 'Pending'}
							onclick={() => {
								controller?.removeFile(row.id);
								fileInput?.focus();
							}}>Remove</Button
						>
					</div>
					{#if row.message}<p class="file-message">{row.message}</p>{/if}
				</li>
			{/each}
		</ul>
		{#if canUpload}
			<Button type="button" variant="outlined" onclick={() => controller?.upload()}>
				{view.files.some((row) => row.outcome === 'Failed') ? 'Retry upload' : 'Upload photos'}
			</Button>
		{/if}
	{/if}
	<p class="status" class:empty={!view.message} role="status" aria-live="polite">{view.message}</p>
</div>

<style>
	.photo-selection { display: grid; gap: 0.75rem; min-width: 0; }
	.selection-heading { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: 0.25rem 0.75rem; }
	.selection-heading h3 { margin: 0; font-size: 1rem; }
	.selection-heading span, .file-help, .file-outcome { font-size: 0.875rem; }
	.file-help { margin: 0; color: var(--m3c-on-surface-variant); }
	.file-picker { position: relative; justify-self: start; display: flex; align-items: center; gap: 0.5rem; }
	.file-input { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
	.choose-photos { display: inline-flex; align-items: center; justify-content: center; gap: 0.4rem; min-height: 2.75rem; padding: 0.5rem 1rem; border: 1px solid var(--m3c-outline); border-radius: 2rem; color: var(--m3c-primary); cursor: pointer; font-weight: 600; }
	.choose-photos.disabled { opacity: 0.5; cursor: default; }
	.file-input:focus-visible + .choose-photos { outline: 3px solid var(--m3c-primary); outline-offset: 3px; }
	.camera-action :global(button) { min-width: 2.75rem; width: 2.75rem; height: 2.75rem; padding: 0; }
	ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 0.5rem; }
	li { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 0.25rem 0.75rem; padding: 0.5rem; border: 1px solid var(--m3c-outline-variant); border-radius: 0.75rem; }
	.photo-preview { width: 3.5rem; height: 3.5rem; display: grid; place-items: center; background: var(--m3c-surface-container); border-radius: 0.5rem; overflow: hidden; }
	.photo-preview.placeholder { border: 1px dashed var(--m3c-outline-variant); }
	.photo-preview img { width: 100%; height: 100%; object-fit: cover; }
	.file-summary { display: grid; gap: 0.1rem; min-width: 0; }
	.file-outcome { color: var(--m3c-on-surface-variant); }
	.file-message { grid-column: 1 / -1; margin: 0; font-size: 0.875rem; }
	.photo-selection > :global(button) { justify-self: start; }
	:global(button) { max-width: 100%; height: auto; min-height: 2.75rem; }
	:global(:focus-visible) { outline: 3px solid var(--m3c-primary, #1a5b9b); outline-offset: 3px; }
	.status { display: grid; gap: 0.25rem; margin: 0; padding: 0.75rem; border-radius: 0.5rem; background: var(--m3c-surface-container); font-size: 0.9rem; }
	.status.empty { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip-path: inset(50%); }
</style>
