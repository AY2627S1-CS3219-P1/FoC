<script lang="ts">
	import { snackbarService } from '$lib/services';
	import { asPhotoUploadApi, unusableEvidenceIds, type CreateEvidenceUpload } from '$lib/services/moderation';
	import {
		ReviewPhotosController,
		type ReviewPhotosSnapshot
	} from '$lib/features/review-photos/controller';
	import PhotoUploader from '$lib/components/photo-uploader/PhotoUploader.svelte';

	// Image evidence for a report, appeal or response. Images upload as soon as
	// they are picked; the form reads the staged evidence IDs when it submits.
	let { upload, disabled = false }: { upload: CreateEvidenceUpload; disabled?: boolean } = $props();

	let view = $state<ReviewPhotosSnapshot>({ files: [], outcome: 'Not Submitted', message: '', busy: false });
	let controller: ReviewPhotosController | undefined = $state();

	$effect(() => {
		const instance = new ReviewPhotosController(asPhotoUploadApi(upload), (snapshot) => {
			view = snapshot;
		});
		controller = instance;
		view = instance.snapshot();
		return () => instance.dispose();
	});

	// Only guidance the user must act on reaches the snackbar.
	let lastMessage = '';
	$effect(() => {
		if (view.message && view.message !== lastMessage) {
			lastMessage = view.message;
			snackbarService.show(view.message, { severity: 'info', timeout: 6000 });
		} else if (!view.message) {
			lastMessage = '';
		}
	});

	/** The staged evidence IDs in order, or null (with guidance shown) while an image is unresolved. */
	export function readyEvidenceIds(): string[] | null {
		const blocker = describeBlocker();
		if (blocker) {
			snackbarService.show(blocker, { severity: 'warning' });
			return null;
		}
		return controller?.photoIds() ?? [];
	}

	/**
	 * Marks the images a failed submission rejected, so the next submission
	 * asks the user to remove them instead of resending their IDs.
	 */
	export function rejectFrom(cause: unknown): void {
		controller?.markNotReady(
			unusableEvidenceIds(cause),
			"This image didn't make it. Remove and re-add it, or submit without it."
		);
	}

	function describeBlocker(): string | null {
		if (view.busy || view.files.some((row) => row.outcome === 'Pending')) {
			return 'Wait for the images to finish uploading, then submit again.';
		}
		if (view.files.some((row) => row.outcome === 'Not Submitted' || row.outcome === 'Failed')) {
			return 'Some images did not upload. Retry or remove them before submitting.';
		}
		if (view.files.some((row) => row.outcome === 'Rejected')) {
			return "Some images can't be used. Remove them before submitting.";
		}
		return null;
	}
</script>

<PhotoUploader {controller} {view} {disabled} title="Evidence" listLabel="Images attached as evidence" />
