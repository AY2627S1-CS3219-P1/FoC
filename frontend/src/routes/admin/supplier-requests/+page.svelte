<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Button, SelectOutlined, LoadingIndicator } from 'm3-svelte';
	import { additionRequestApi } from '$lib/services';
	import type { RequestPage } from '$lib/services/addition-request/requestApi';
	import {
		requestStatuses,
		requestStatusLabel,
		pageFromParams,
		formatTimestamp
	} from '$lib/services/location-admin/format';
	import { adminError } from '$lib/services/location-admin/errors';
	import Pagination from '$lib/components/admin/Pagination.svelte';
	let result = $state<RequestPage | null>(null),
		loading = $state(true),
		error = $state(''),
		reload = $state(0);
	const status = $derived(
		requestStatuses.some((s) => s.value === page.url.searchParams.get('status'))
			? Number(page.url.searchParams.get('status'))
			: 1
	);
	const currentPage = $derived(pageFromParams(page.url.searchParams));
	$effect(() => {
		const filter = status,
			number = currentPage;
		void reload;
		let stale = false;
		loading = true;
		error = '';
		result = null;
		additionRequestApi
			.list(filter, number)
			.then((value) => {
				if (!stale) result = value;
			})
			.catch((cause) => {
				if (!stale) error = adminError(cause, 'request');
			})
			.finally(() => {
				if (!stale) loading = false;
			});
		return () => {
			stale = true;
		};
	});
	function update(nextStatus = status, nextPage = 1) {
		void goto(`?status=${nextStatus}&page=${nextPage}`, { keepFocus: true, noScroll: true });
	}
</script>

<svelte:head><title>Addition requests · Friend on Campus</title></svelte:head>
<section class="page-content admin-content" aria-labelledby="requests-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1 id="requests-title">Addition requests</h1>
			<p>Review proposed suppliers and campus facilities.</p>
		</div>
		<Button variant="outlined" disabled={loading} onclick={() => reload++}>Refresh queue</Button>
	</header>
	<div class="field">
		<SelectOutlined
			label="Request status"
			options={requestStatuses}
			value={String(status)}
			onchange={(e) => update(Number(e.currentTarget.value))}
		/>
	</div>
	{#if error}<p class="notice error" role="alert">{error}</p>
		<Button onclick={() => reload++}>Retry requests</Button>
	{:else if loading}<div class="loading-state" role="status">
			<LoadingIndicator aria-label="Loading requests" />
			<p>Loading requests…</p>
		</div>
	{:else if result}
		<p class="secondary-text" role="status">{String(result.pageInfo.totalItems)} requests</p>
		{#if !result.requests.length}<p class="notice" role="status">No requests on this page.</p>{/if}
		<ul class="record-list">
			{#each result.requests as request (request.id)}
				<li class="record-row">
					<div>
						<h2>
							<a href={`/admin/supplier-requests/${request.id}`}
								>{request.proposal?.name ?? 'Unnamed proposal'}</a
							>
						</h2>
						<p>
							{request.proposal?.isSupplier ? 'Supplier' : 'Facility'} · {formatTimestamp(
								request.createdAt
							)} (SGT)
						</p>
						<span class="record-state">{requestStatusLabel(request.status)}</span>
					</div>
					<Button variant="outlined" href={`/admin/supplier-requests/${request.id}`}
						>{request.status === 1 ? 'Review' : 'View'}</Button
					>
				</li>
			{/each}
		</ul>
		<Pagination
			page={result.pageInfo.page}
			totalPages={result.pageInfo.totalPages}
			{loading}
			onchange={(next) => update(status, next)}
		/>
	{/if}
</section>
