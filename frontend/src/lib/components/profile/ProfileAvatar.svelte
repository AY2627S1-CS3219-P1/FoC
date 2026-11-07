<script lang="ts">
	// Signed profile-picture URLs expire. The caller may refresh the profile
	// once when the image fails; the initials remain the fallback meanwhile.
	let {
		url,
		name,
		size = 40,
		onerror
	}: {
		url?: string;
		name: string;
		size?: number;
		onerror?: () => void;
	} = $props();

	let broken = $state(false);
	$effect(() => {
		// Reset the fallback whenever a fresh URL arrives.
		void url;
		broken = false;
	});

	const initials = $derived.by(() => {
		const words = name.trim().split(/\s+/).filter(Boolean);
		if (!words.length) return '?';
		const first = Array.from(words[0])[0] ?? '';
		const last = words.length > 1 ? (Array.from(words[words.length - 1])[0] ?? '') : '';
		return (first + last).toUpperCase();
	});
</script>

{#if url && !broken}
	<img
		class="avatar"
		src={url}
		alt={`${name} profile picture`}
		width={size}
		height={size}
		style={`--avatar-size: ${size}px`}
		loading="lazy"
		referrerpolicy="no-referrer"
		onerror={() => {
			broken = true;
			onerror?.();
		}}
	/>
{:else}
	<span
		class="avatar initials"
		style={`--avatar-size: ${size}px`}
		aria-label={`${name} profile picture`}
		role="img">{initials}</span
	>
{/if}

<style>
	.avatar {
		display: inline-grid;
		place-items: center;
		width: var(--avatar-size);
		height: var(--avatar-size);
		flex: 0 0 var(--avatar-size);
		border-radius: 50%;
		overflow: hidden;
		background: var(--m3c-secondary-container);
		color: var(--m3c-on-secondary-container);
		object-fit: cover;
		font-size: calc(var(--avatar-size) * 0.38);
		font-weight: 650;
	}
	.initials {
		user-select: none;
	}
</style>
