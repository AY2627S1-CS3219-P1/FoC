<script lang="ts">
	import { goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, TextFieldOutlined } from 'm3-svelte';
	import { defaults, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { adminService } from '$lib/services';
	import { recordIdSchema } from '$lib/services/admin/adminSchemas';
	import { roleLabel } from '$lib/services/admin/roles';
	import type { UserProfile } from '$lib/services/profile/profileApi';
	import { describeDashboardError } from './loadable.svelte';

	let user = $state<UserProfile | null>(null);
	let userError = $state('');

	// Single orders open on the order page, which reads them through GetOrder with admin redaction.
	const order = superForm(defaults(zod4(recordIdSchema)), {
		id: 'order-lookup',
		SPA: true,
		validators: zod4Client(recordIdSchema),
		validationMethod: 'onblur',
		resetForm: false,
		async onUpdate({ form }) {
			if (form.valid) await goto(`/orders/${encodeURIComponent(form.data.id)}`);
		}
	});

	const userLookup = superForm(defaults(zod4(recordIdSchema)), {
		id: 'user-lookup',
		SPA: true,
		validators: zod4Client(recordIdSchema),
		validationMethod: 'onblur',
		multipleSubmits: 'prevent',
		resetForm: false,
		onChange() {
			user = null;
			userError = '';
		},
		async onUpdate({ form }) {
			if (!form.valid) return;
			userError = '';
			try {
				user = await adminService.getUser(form.data.id);
			} catch (cause) {
				user = null;
				userError =
					cause instanceof ConnectError && cause.code === Code.NotFound
						? 'No user has that ID.'
						: describeDashboardError(cause, 'that user');
			}
		}
	});

	const { form: orderForm, errors: orderErrors, enhance: enhanceOrder } = order;
	const {
		form: userForm,
		errors: userErrors,
		submitting: findingUser,
		enhance: enhanceUser
	} = userLookup;
</script>

<Card variant="outlined">
	<div class="panel-body">
		<div class="card-intro">
			<h2>Open a record</h2>
			<p class="secondary-text">Look up a single order or user by its ID.</p>
		</div>

		<form class="lookup-form" method="POST" use:enhanceOrder aria-label="Open an order">
			<div class="field">
				<TextFieldOutlined
					label="Order ID"
					name="id"
					autocomplete="off"
					spellcheck={false}
					required
					bind:value={$orderForm.id}
					error={!!$orderErrors.id}
					aria-invalid={$orderErrors.id ? 'true' : undefined}
					aria-describedby={$orderErrors.id ? 'order-lookup-error' : undefined}
				/>
				{#if $orderErrors.id}<small id="order-lookup-error" class="field-error"
						>{$orderErrors.id[0]}</small
					>{/if}
			</div>
			<Button type="submit" variant="tonal" disabled={!$orderForm.id.trim()}>Open order</Button>
		</form>

		<form class="lookup-form" method="POST" use:enhanceUser aria-label="Find a user">
			<div class="field">
				<TextFieldOutlined
					label="User ID"
					name="id"
					autocomplete="off"
					spellcheck={false}
					required
					bind:value={$userForm.id}
					error={!!$userErrors.id}
					aria-invalid={$userErrors.id ? 'true' : undefined}
					aria-describedby={$userErrors.id ? 'user-lookup-error' : undefined}
				/>
				{#if $userErrors.id}<small id="user-lookup-error" class="field-error"
						>{$userErrors.id[0]}</small
					>{/if}
			</div>
			<Button type="submit" variant="tonal" disabled={$findingUser || !$userForm.id.trim()}
				>{$findingUser ? 'Finding…' : 'Find user'}</Button
			>
		</form>

		{#if userError}<p class="notice error" role="alert">{userError}</p>{/if}
		{#if user}
			<dl class="admin-facts" aria-label="User profile">
				<div><dt>Name</dt><dd>{user.displayName}</dd></div>
				<div><dt>Email</dt><dd>{user.email}</dd></div>
				<div><dt>Role</dt><dd>{roleLabel(user.role)}</dd></div>
				<div><dt>Phone</dt><dd>{user.phoneNumber ?? 'Not provided'}</dd></div>
				<div><dt>Description</dt><dd>{user.description || 'None'}</dd></div>
				<div><dt>ID</dt><dd>{user.id}</dd></div>
			</dl>
			<p class="secondary-text">
				To change this user's role, search for {user.email} on <a href="/admin/users">Users</a>.
			</p>
		{/if}
	</div>
</Card>

<style>
	.lookup-form {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		align-items: start;
		gap: 0.75rem;
	}

	.lookup-form > :global(button) {
		margin-top: 0.5rem;
	}

	@media (max-width: 36rem) {
		.lookup-form {
			grid-template-columns: 1fr;
		}

		.lookup-form > :global(button) {
			margin-top: 0;
		}
	}
</style>
