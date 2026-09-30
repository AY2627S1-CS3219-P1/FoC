<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, Dialog, SelectOutlined, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import type { AdminUserSummary } from '$lib/services/admin/adminApi';
	import { adminService, authService } from '$lib/services';
	import { roleChangeSchema, userSearchSchema, type RoleChangeForm } from '$lib/services/admin/adminSchemas';
	import { allowedRoleTargets, roleLabel, roleValue } from '$lib/services/admin/roles';

	let user = $state<AdminUserSummary | null>(null);
	let searchError = $state('');
	let confirmOpen = $state(false);
	let confirmApproved = false;
	let roleStatus = $state('');
	let roleFormElement: HTMLFormElement | undefined = $state();
	const emptyRoleForm: RoleChangeForm = { userId: '', fromRole: UserRole.USER, toRole: 'suspended_user', reason: '' };
	let choices = $derived(user ? allowedRoleTargets(authService.user?.role ?? UserRole.USER, user.role, user.id === authService.user?.id) : []);

	function displayApiError(cause: unknown): string {
		if (cause instanceof ConnectError) {
			if (cause.code === Code.NotFound) return 'No account was found with that email address.';
			if (cause.code === Code.PermissionDenied) return 'You no longer have permission to manage user roles. Refresh your session or contact a super admin.';
			if (cause.code === Code.InvalidArgument) return 'The request was rejected. Check the fields and try again.';
			if (cause.code === Code.FailedPrecondition) return 'That account already has the selected role. Search again to refresh its details.';
		}
		return 'We could not complete that request. Check your connection and try again.';
	}

	function resetRole(target: AdminUserSummary | null) {
		user = target;
		roleStatus = '';
		confirmOpen = false;
		confirmApproved = false;
		const available = target ? allowedRoleTargets(authService.user?.role ?? UserRole.USER, target.role, target.id === authService.user?.id) : [];
		const next: RoleChangeForm = target ? {
			userId: target.id,
			fromRole: target.role,
			toRole: available[0] ?? 'user',
			reason: ''
		} : emptyRoleForm;
		role.reset({ data: next, newState: next });
		return next;
	}

	async function refreshAdminAccess() {
		try {
			await authService.refresh();
			if (authService.user?.role !== UserRole.ADMIN && authService.user?.role !== UserRole.SUPER_ADMIN) {
				await goto('/', { replaceState: true });
			}
		} catch {
			// Keep the current page available so the user can retry after transient failures.
		}
	}

	const search = superForm(defaults(zod4(userSearchSchema)), {
		SPA: true,
		validators: zod4Client(userSearchSchema),
		validationMethod: 'onblur',
		multipleSubmits: 'prevent',
		resetForm: false,
		onChange({ paths }) {
			if (paths.includes('email')) {
				resetRole(null);
				searchError = '';
			}
		},
		async onUpdate({ form }) {
			if (!form.valid) return;
			searchError = '';
			try {
				const found = await adminService.getUserByEmail(form.data.email);
				resetRole(found);
			} catch (cause) {
				resetRole(null);
				searchError = displayApiError(cause);
				if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) void refreshAdminAccess();
			}
		}
	});

	const role = superForm(defaults(emptyRoleForm, zod4(roleChangeSchema)), {
		SPA: true,
		validators: zod4Client(roleChangeSchema),
		validationMethod: 'onblur',
		multipleSubmits: 'prevent',
		resetForm: false,
		taintedMessage: true,
		async onUpdate({ form }) {
			if (!form.valid) return;
			if (!confirmApproved) {
				confirmOpen = true;
				return;
			}
			confirmApproved = false;
			try {
				const updated = await adminService.changeUserRole(
					form.data.userId,
					roleValue(form.data.toRole),
					form.data.reason
				);
				form.data = resetRole(updated);
				setMessage(form, `Role updated to ${roleLabel(updated.role)}.`);
			} catch (cause) {
				if (cause instanceof ConnectError && cause.code === Code.Aborted && user) {
					try {
						const latest = await adminService.getUserByEmail(user.email);
						resetRole(latest);
						roleStatus = 'This user’s role changed while you were reviewing it. Review the updated account and confirm again.';
					} catch (refreshCause) {
						roleStatus = displayApiError(refreshCause);
					}
				} else {
					setMessage(form, displayApiError(cause));
					if (cause instanceof ConnectError && cause.code === Code.PermissionDenied) void refreshAdminAccess();
				}
			}
		}
	});

	const { form: searchForm, errors: searchErrors, submitting: searching, enhance: enhanceSearch } = search;
	const { form: roleForm, errors: roleErrors, constraints: roleConstraints, message: roleMessage, submitting: changingRole, enhance: enhanceRole } = role;

	function confirmChange() {
		confirmOpen = false;
		confirmApproved = true;
		role.submit(roleFormElement);
	}

	onMount(() => {
		void (async () => {
			if (!authService.isAuthenticated) {
				await goto('/login', { replaceState: true });
			} else if (authService.user?.role !== UserRole.ADMIN && authService.user?.role !== UserRole.SUPER_ADMIN) {
				await goto('/', { replaceState: true });
			}
		})();
	});

</script>

{#snippet dialogButtonSnippet()}
	<Button type="button" variant="text" onclick={() => (confirmOpen = false)}>Cancel</Button>
	<Button type="button" onclick={confirmChange} disabled={$changingRole}>{$changingRole ? 'Updating…' : 'Confirm role change'}</Button>
{/snippet}

<svelte:head><title>Manage users · Friend on Campus</title></svelte:head>

<section class="page-content admin-page" aria-labelledby="admin-title">
	<header class="page-heading"><div><p class="eyebrow">Administration</p><h1 id="admin-title">Manage user roles</h1><p>Find an account by its exact email address, then review its current role before making a change.</p></div></header>

	<Card variant="outlined">
		<div class="panel-body">
			<form class="search-form" method="POST" use:enhanceSearch>
				<TextFieldOutlined label="User email address" type="email" name="email" autocomplete="email" required bind:value={$searchForm.email} error={!!$searchErrors.email} aria-invalid={$searchErrors.email ? 'true' : undefined} />
				{#if $searchErrors.email}<small class="field-error">{$searchErrors.email[0]}</small>{/if}
				<div class="form-actions"><Button type="submit" disabled={$searching || !!$searchErrors.email}>{$searching ? 'Searching…' : 'Find user'}</Button></div>
			</form>
			{#if searchError}<p class="notice error" role="alert">{searchError}</p>{/if}
		</div>
	</Card>

	{#if user}
		<Card variant="outlined">
			<div class="panel-body">
				<div class="user-summary"><div><p class="eyebrow">Account found</p><h2>{user.displayName}</h2><p>{user.email}</p></div><span class="role-chip">{roleLabel(user.role)}</span></div>
			{#if choices.length === 0}
				<p class="notice" role="status">{user.id === authService.user?.id ? 'You cannot change your own role.' : user.role === UserRole.SUPER_ADMIN ? 'A super admin role cannot be changed here.' : 'No role changes are available for this account.'}</p>
			{:else}
				{#if roleStatus}<p class="notice" role="status">{roleStatus}</p>{/if}
				{#if $roleMessage}<p class="notice" role="status" aria-live="polite">{$roleMessage}</p>{/if}
				<form class="data-form" method="POST" use:enhanceRole bind:this={roleFormElement}>
					<SelectOutlined label="New role" name="toRole" bind:value={$roleForm.toRole} options={choices.map((choice) => ({ value: choice, text: roleLabel(roleValue(choice)) }))} />
					<div class="field"><TextFieldOutlinedMultiline label="Reason · required for suspending or reinstating" name="reason" rows={3} bind:value={$roleForm.reason} maxlength={Number($roleConstraints.reason?.maxlength ?? 2000)} error={!!$roleErrors.reason} aria-invalid={$roleErrors.reason ? 'true' : undefined} /><small class="field-meta">{Array.from($roleForm.reason).length}/2,000</small>{#if $roleErrors.reason}<small class="field-error">{$roleErrors.reason[0]}</small>{/if}</div>
					<div class="form-actions"><Button type="submit" disabled={$changingRole || $roleForm.toRole === (user.role === UserRole.ADMIN ? 'admin' : user.role === UserRole.USER ? 'user' : 'suspended_user') || !!$roleErrors.toRole}>{$changingRole ? 'Updating…' : 'Review role change'}</Button></div>
				</form>
			{/if}
		</div>
	</Card>
	{/if}
</section>

<Dialog bind:open={confirmOpen} headline="Confirm role change" buttons={dialogButtonSnippet}>
	{#if user}
		<p>Change <strong>{user.displayName}</strong> ({user.email}) from <strong>{roleLabel(user.role)}</strong> to <strong>{roleLabel(roleValue($roleForm.toRole))}</strong>?</p>
		{#if $roleForm.reason}<p>Reason: {$roleForm.reason}</p>{/if}
	{/if}
</Dialog>
