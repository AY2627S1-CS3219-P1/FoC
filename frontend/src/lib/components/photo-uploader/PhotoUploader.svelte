<script lang="ts">
	import { SvelteMap } from 'svelte/reactivity';
	import { Button, Icon } from 'm3-svelte';
	import addPhotoIcon from '@ktibow/iconset-material-symbols/add-photo-alternate-outline-rounded';
	import cameraIcon from '@ktibow/iconset-material-symbols/photo-camera';
	import closeIcon from '@ktibow/iconset-material-symbols/close-rounded';
	import alertIcon from '@ktibow/iconset-material-symbols/error-rounded';
	import type {
		ReviewPhotosController,
		ReviewPhotosSnapshot
	} from '$lib/features/review-photos/controller';
	import { IMAGE_CONTENT_TYPES } from '$lib/features/image-upload/limits';

	// Shared photo uploader: selecting files starts their upload immediately.
	// Photos are square thumbnails with a remove control; a failed upload is
	// covered by a red overlay and every failed photo is retried together, so
	// there is one "Retry photo uploads" action instead of per-photo retries.
	// The backend reaper collects staged uploads that are removed or abandoned.
	let {
		controller,
		view,
		disabled = false,
		title = 'Photos',
		listLabel = 'Photos attached to this review'
	}: {
		controller: ReviewPhotosController | undefined;
		view: ReviewPhotosSnapshot;
		disabled?: boolean;
		/** Heading above the picker; evidence forms call these images. */
		title?: string;
		listLabel?: string;
	} = $props();
	let fileInput: HTMLInputElement | undefined = $state();
	let cameraInput: HTMLInputElement | undefined = $state();

	// Previews are browser object URLs; they must be revoked when a row or the
	// uploader itself goes away.
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
</script>

<div class="photo-uploader">
	<div class="head">
		<h3>{title}</h3>
		<span>{view.files.length} / 5</span>
	</div>
	<p id="photo-uploader-help" class="help">
		Optional. Up to 5 photos. JPEG, PNG, WebP, HEIC or HEIF, up to 10 MB each.
	</p>
	<div class="controls">
		<input
			bind:this={fileInput}
			id="photo-uploader-files"
			aria-describedby="photo-uploader-help"
			class="hidden-input"
			type="file"
			accept={IMAGE_CONTENT_TYPES.join(',')}
			multiple
			disabled={busy}
			onchange={(event) => {
				controller?.addFiles(Array.from(event.currentTarget.files ?? []));
				event.currentTarget.value = '';
			}}
		/>
		<Button type="button" variant="tonal" disabled={busy} onclick={() => fileInput?.click()}>
			<Icon icon={addPhotoIcon} size={20} aria-hidden="true" /> Add photos
		</Button>
		<input
			bind:this={cameraInput}
			id="photo-uploader-camera"
			type="file"
			accept="image/*"
			capture="environment"
			hidden
			disabled={busy}
			onchange={(event) => {
				const shot = event.currentTarget.files?.[0];
				if (shot) controller?.addFiles([shot]);
				event.currentTarget.value = '';
			}}
		/>
		<Button
			type="button"
			variant="tonal"
			iconType="full"
			aria-label="Take photo"
			title="Take photo"
			disabled={busy}
			onclick={() => cameraInput?.click()}
		>
			<Icon icon={cameraIcon} size={24} aria-hidden="true" />
		</Button>
	</div>
	{#if view.files.length > 0}
		<ul class="thumbnails" aria-label={listLabel}>
			{#each view.files as row, index (row.id)}
				{@const failed = row.outcome === 'Failed' || row.outcome === 'Rejected'}
				{@const stateLabel =
					row.outcome === 'Succeeded'
						? 'uploaded'
						: row.outcome === 'Pending'
							? 'uploading'
							: failed
								? 'failed to upload'
								: 'not uploaded yet'}
				<li class:failed>
					<span class="thumb" aria-label={`Photo ${index + 1}, ${stateLabel}`}>
						{#if previews.get(row.id)}
							<img src={previews.get(row.id)} alt="" />
						{:else}
							<span class="placeholder" aria-hidden="true"></span>
						{/if}
						{#if failed}
							<span class="failure-overlay" aria-hidden="true">
								<Icon icon={alertIcon} size={30} />
							</span>
						{/if}
					</span>
					<button
						type="button"
						class="remove"
						aria-label={`Remove photo ${index + 1}`}
						title="Remove photo"
						disabled={busy || row.outcome === 'Pending'}
						onclick={() => controller?.removeFile(row.id)}
					>
						<Icon icon={closeIcon} size={18} aria-hidden="true" />
					</button>
				</li>
			{/each}
		</ul>
		{#if view.files.some((row) => row.outcome === 'Failed')}
			<div class="retry">
				<Button type="button" variant="text" disabled={busy} onclick={() => controller?.retryFailed()}>
					Retry photo uploads
				</Button>
			</div>
		{/if}
	{/if}
</div>

<style>
	.photo-uploader { display: grid; gap: 0.75rem; min-width: 0; }
	.head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: 0.25rem 0.75rem; }
	.head h3 { margin: 0; font-size: 1rem; }
	.head span, .help { font-size: 0.875rem; }
	.help { margin: 0; color: var(--m3c-on-surface-variant); }
	.controls { justify-self: start; display: flex; align-items: center; gap: 0.5rem; }
	.hidden-input { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
	.thumbnails { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 0.5rem; }
	.thumbnails li { position: relative; width: 5.5rem; height: 5.5rem; }
	.thumb { display: block; width: 100%; height: 100%; border-radius: 0.6rem; overflow: hidden; background: var(--m3c-surface-container); }
	.thumb img { width: 100%; height: 100%; object-fit: cover; }
	.thumb .placeholder { display: block; width: 100%; height: 100%; border: 1px dashed var(--m3c-outline-variant); border-radius: 0.6rem; }
	.failure-overlay { position: absolute; inset: 0; display: grid; place-items: center; border-radius: 0.6rem; background: rgb(139 0 0 / 72%); color: #fff; }
	.remove { position: absolute; top: 0.2rem; right: 0.2rem; display: grid; place-items: center; width: 1.5rem; height: 1.5rem; padding: 0; border: 0; border-radius: 999px; background: rgb(0 0 0 / 55%); color: #fff; cursor: pointer; }
	.remove:disabled { opacity: 0.4; cursor: default; }
	.remove:focus-visible { outline: 2px solid var(--m3c-primary); outline-offset: 2px; }
	.retry { justify-self: start; }
</style>
