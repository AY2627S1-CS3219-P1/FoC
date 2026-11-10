<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, Card, LoadingIndicator } from 'm3-svelte';
	import { appealApi, authService } from '$lib/services';
	import { describeModerationError } from '$lib/services/moderation';
	import { decisionKindLabels, type AppealPage } from '$lib/services/appeal/appealApi';
	import CaseStatusChip from '$lib/components/moderation/CaseStatusChip.svelte';
	import Pagination from '$lib/components/admin/Pagination.svelte';

	let result = $state<AppealPage | null>(null);
	let loading = $state(true);
	let error = $state('');
	let reload = $state(0);

	const callerId = $derived(authService.user?.id ?? '');
	const currentPage = $derived(Math.max(1, Number(page.url.searchParams.get('page')) || 1));
	const date = new Intl.DateTimeFormat('en-SG', { dateStyle: 'medium' });

	$effect(() => {
		const number = currentPage;
		void reload;
		let stale = false;
		loading = true;
		error = '';
		appealApi
			.listMyAppeals({ page: number })
			.then((value) => {
				if (!stale) result = value;
			})
			.catch((cause) => {
				if (!stale) error = describeModerationError(cause, 'load your appeals');
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});
</script>

<svelte:head><title>Your appeals · Friend on Campus</title></svelte:head>

<section class="page-content" aria-labelledby="appeals-title">
	<div><Button variant="text" href="/profile">← Your profile</Button></div>
	<header class="page-heading">
		<div>
			<p class="eyebrow">Your account</p>
			<h1 id="appeals-title">Your appeals</h1>
			<p>Appeals you filed, and appeals about decisions that also affect you.</p>
		</div>
		<Button href="/appeals/new">New appeal</Button>
	</header>

	{#if loading && !result}
		<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading appeals" /><p>Loading your appeals…</p></div>
	{:else if error}
		<Card variant="outlined">
			<div class="panel-body">
				<p class="notice error" role="alert">{error}</p>
				<div class="form-actions"><Button onclick={() => reload++}>Try again</Button></div>
			</div>
		</Card>
	{:else if result && result.appeals.length === 0}
		<Card variant="outlined"><div class="panel-body"><p>You have no appeals.</p></div></Card>
	{:else if result}
		<ul class="record-list">
			{#each result.appeals as appeal (appeal.id)}
				{@const kinds = [...new Set(appeal.decisions.map((decision) => decisionKindLabels[decision.target.kind]))]}
				<li class="record-row">
					<div>
						<h2>
							<a href={`/appeals/${appeal.id}`}>
								{appeal.appellantId === callerId ? 'Your appeal' : `Appeal by ${appeal.appellantName || 'a user'}`}
							</a>
						</h2>
						<p>{kinds.join(', ')} · {date.format(appeal.createdAt)}</p>
						<CaseStatusChip status={appeal.status} />
					</div>
					<Button variant="outlined" href={`/appeals/${appeal.id}`}>View</Button>
				</li>
			{/each}
		</ul>
		<Pagination
			page={result.page}
			totalPages={result.totalPages}
			{loading}
			onchange={(next) => void goto(`?page=${next}`, { keepFocus: true, noScroll: true })}
		/>
	{/if}
</section>
