<script lang="ts">
	import '../app.css';

	let { children } = $props();
</script>

<svelte:head>
	<title>Friend on Campus</title>
	<meta
		name="description"
		content="Find campus suppliers and connect with students who can help with errands."
	/>
</svelte:head>

<div class="app-shell">
	<header class="site-header">
		<a class="brand" href="/" aria-label="Friend on Campus home">Friend on Campus</a>
		<nav aria-label="Main navigation">
			<a href="/suppliers">Suppliers</a>
			<a href="/login">Log in</a>
		</nav>
	</header>

	<main id="main-content">
		{@render children()}
	</main>
</div>
