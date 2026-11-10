<script lang="ts">
	import { page } from '$app/state';
	import SignedImage from '$lib/components/image/SignedImage.svelte';
	import { authService, reviewApi } from '$lib/services';
</script>

<svelte:head><title>Review photo</title></svelte:head>
<main>
	<h1>Review photo</h1>
	<div class="photo">
		<SignedImage objectId={page.params.photoId ?? ''} sessionKey={`${authService.user?.id}:${authService.user?.role}`}
			refresh={(id, signal) => reviewApi.getPhotoDownload(id, signal)}
			alt="Review photo" loading="eager" fit="contain" />
	</div>
</main>
<style>
	main { padding: 1rem; }
	h1 { font-size: 1.5rem; }
	.photo { width: 100%; height: 75vh; }
</style>
