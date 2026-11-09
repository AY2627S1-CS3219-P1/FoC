<script lang="ts">
	import SignedImage from '../src/lib/components/image/SignedImage.svelte';
	import type { ImageGrant } from '../src/lib/components/image/types';
	let objectId = $state('first');
	let sessionKey = $state('one');
	let mounted = $state(true);
	const lazy = new URLSearchParams(location.search).has('lazy');
	const initialGrant = new URLSearchParams(location.search).has('viewer') ? undefined : { url: '/image/initial', expiresAt: 1 };
	const refresh = async (id: string, signal: AbortSignal): Promise<ImageGrant> => {
		const response = await fetch(`/grant/${id}`, { signal });
		if (!response.ok) throw new Error('Denied');
		return response.json();
	};
</script>
<button onclick={() => objectId = 'second'}>Change image</button>
<button onclick={() => sessionKey = 'two'}>Change session</button>
<button onclick={() => mounted = false}>Unmount</button>
{#if mounted}
	<div style:margin-top={lazy ? '5000px' : '0'} style:width="100px" style:height="100px">
		<SignedImage {objectId} {sessionKey} {initialGrant} {refresh} alt="Test photo" loading={lazy ? 'lazy' : 'eager'} />
	</div>
{/if}
