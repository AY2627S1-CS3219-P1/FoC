<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { getApiErrorMessage } from '$lib/connect/errors';
	import { Button, Card, Icon, LoadingIndicator, TextFieldOutlined } from 'm3-svelte';
	import iconAdd from '@ktibow/iconset-material-symbols/add';
	import iconDelete from '@ktibow/iconset-material-symbols/delete-outline';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import ConfirmationDialog from '$lib/components/ConfirmationDialog.svelte';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import { allowedDomainService, authService, snackbarService } from '$lib/services';
	import type { AllowedDomain } from '$lib/services/allowedDomain/allowedDomainApi';
	import { allowedDomainSchema } from '$lib/services/allowedDomain/allowedDomainSchema';

	let domains = $state<AllowedDomain[]>([]);
	let loading = $state(true);
	let loadFailed = $state(false);
	let adding = $state(false);
	let deleting = $state(false);
	let selected = $state<AllowedDomain | null>(null);
	let confirmOpen = $state(false);
	let canManage = $derived(authService.user?.role === UserRole.SUPER_ADMIN);
	let mutating = $derived(adding || deleting);

	async function loadDomains(outcomeMessage = ''): Promise<void> {
		loading = true;
		loadFailed = false;
		try {
			domains = await allowedDomainService.listAllowedDomains();
			if (outcomeMessage) snackbarService.show(outcomeMessage);
		} catch (cause) {
			loadFailed = true;
			const message = [outcomeMessage, getApiErrorMessage(cause)].filter(Boolean).join(' ');
			snackbarService.show(message, { actions: { Retry: () => void loadDomains(outcomeMessage) }, timeout: 8000 });
		} finally {
			loading = false;
		}
	}

	const add = superForm(defaults(zod4(allowedDomainSchema)), {
		SPA: true,
		validators: zod4Client(allowedDomainSchema),
		validationMethod: 'onblur',
		multipleSubmits: 'prevent',
		resetForm: false,
		onSubmit({ cancel }) {
			if (!canManage || mutating || loading) cancel();
		},
		async onUpdate({ form: validated }) {
			if (!validated.valid || !canManage || mutating || loading) return;
			adding = true;
			try {
				await allowedDomainService.allowDomain(validated.data.domain);
				const message = `${validated.data.domain} is now allowed.`;
				const empty = { domain: '' };
				add.reset({ data: empty, newState: empty });
				validated.data = empty;
				await loadDomains(message);
			} catch (cause) {
				snackbarService.show(getApiErrorMessage(cause), { timeout: 8000 });
			} finally {
				adding = false;
			}
		}
	});
	const { form, errors, submitting, enhance } = add;

	function reviewRemoval(domain: AllowedDomain) {
		if (!canManage || mutating || loading) return;
		selected = domain;
		confirmOpen = true;
	}

	async function removeDomain() {
		if (!selected || !canManage || mutating) return;
		const target = selected;
		deleting = true;
		let message = `${target.domain} has been removed from allowed domains.`;
		try {
			await allowedDomainService.removeAllowedDomain(target.id);
		} catch (cause) {
			if (cause instanceof ConnectError && cause.code === Code.NotFound) {
				message = getApiErrorMessage(cause);
			} else {
				confirmOpen = false;
				selected = null;
				// Close the modal before notifying so the snackbar is accessible.
				await tick();
				snackbarService.show(getApiErrorMessage(cause), { timeout: 8000 });
				deleting = false;
				return;
			}
		}
		confirmOpen = false;
		selected = null;
		domains = domains.filter((domain) => domain.id !== target.id);
		await tick();
		await loadDomains(message);
		deleting = false;
	}

	$effect(() => {
		if (!canManage) {
			confirmOpen = false;
			selected = null;
		}
	});
	onMount(() => { void loadDomains(); });
</script>

<Card variant="outlined">
	<div class="panel-body">
		<div class="card-heading">
			<h2 id="allowed-domains-title">Allowed domains</h2>
			<p>Accounts must use an allowed email domain to access the application. Super-admin accounts are exempt.</p>
		</div>
		{#if canManage}
			<form class="add-domain-form" method="POST" use:enhance aria-label="Add allowed domain">
				<div class="field">
					<TextFieldOutlined label="Email domain" name="domain" placeholder="example.com" autocomplete="off" autocapitalize="none" spellcheck={false} required maxlength={254} bind:value={$form.domain} disabled={mutating || loading} error={!!$errors.domain} aria-invalid={$errors.domain ? 'true' : undefined} aria-describedby={$errors.domain ? 'domain-error' : undefined} />
					{#if $errors.domain}<small id="domain-error" class="field-error">{$errors.domain[0]}</small>{/if}
				</div>
				<Button type="submit" square iconType="full" aria-label="Add allowed domain" title="Add allowed domain" aria-busy={adding} disabled={mutating || loading || $submitting || !$form.domain.trim()}><Icon icon={iconAdd} size={24} aria-hidden="true" /></Button>
			</form>
		{/if}

		{#if loadFailed}
			<div class="form-actions"><Button variant="outlined" disabled={loading || mutating} onclick={() => void loadDomains()}>Try again</Button></div>
		{/if}
		{#if loading}
			<div class="loading-state" role="status" aria-live="polite"><LoadingIndicator aria-label="Loading allowed domains" /><p>Loading allowed domains…</p></div>
		{:else if domains.length > 0}
			<div class="table-scroll">
				<table aria-labelledby="allowed-domains-title">
					<thead><tr><th scope="col">Domain</th>{#if canManage}<th scope="col" class="actions">Actions</th>{/if}</tr></thead>
					<tbody>
						{#each domains as domain (domain.id)}
							<tr>
								<td class="domain-name">{domain.domain}</td>
								{#if canManage}<td class="actions"><Button variant="text" iconType="full" disabled={mutating} aria-label={`Delete ${domain.domain}`} title={`Delete ${domain.domain}`} onclick={() => reviewRemoval(domain)}><Icon icon={iconDelete} size={24} aria-hidden="true" /></Button></td>{/if}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{:else if !loadFailed}
			<p class="empty-state">No email domains are currently allowed.</p>
		{/if}
	</div>
</Card>

{#if canManage}
	<ConfirmationDialog bind:open={confirmOpen} headline="Remove allowed domain?" confirmLabel="Remove domain" pending={deleting} pendingLabel="Removing…" onconfirm={() => void removeDomain()}>
		{#if selected}
			<p>Remove <strong>{selected.domain}</strong> from allowed domains?</p>
			<p>Accounts using this domain will no longer pass domain checks for sign-in, registration, link requests, or session refresh. Super-admin accounts are exempt.</p>
		{/if}
	</ConfirmationDialog>
{/if}

<style>
	.card-heading h2 { margin: 0 0 0.5rem; font-size: 1.35rem; }
	.card-heading p { margin: 0; line-height: 1.5; color: var(--m3c-on-surface-variant); }
	.empty-state { margin: 0; color: var(--m3c-on-surface-variant); }
	.add-domain-form { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: start; gap: 0.75rem; }
	.add-domain-form > :global(button) { margin-top: 0.5rem; }
	.table-scroll { overflow-x: auto; }
	table { width: 100%; border-collapse: collapse; text-align: left; }
	th, td { padding: 0.75rem 0.5rem; border-bottom: 1px solid var(--m3c-outline-variant); }
	th { color: var(--m3c-on-surface-variant); font-size: 0.875rem; }
	.domain-name { overflow-wrap: anywhere; }
	.actions { width: 1%; white-space: nowrap; text-align: right; }
</style>
