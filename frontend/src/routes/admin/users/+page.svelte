<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { Button, Card, SelectOutlined, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
	import ConfirmationDialog from '$lib/components/ConfirmationDialog.svelte';
	import { defaults, setMessage, superForm } from 'sveltekit-superforms';
	import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
	import { UserRole } from '$lib/gen/user/v1/auth_pb';
	import type { AdminUserSummary } from '$lib/services/admin/adminApi';
	import { adminService, authService } from '$lib/services';
	import {
		roleChangeSchema,
		userSearchSchema,
		userDeletionSchema,
		type RoleChangeForm
	} from '$lib/services/admin/adminSchemas';
	import { allowedRoleTargets, canDeleteUser, roleLabel, roleValue } from '$lib/services/admin/roles';

	let user = $state<AdminUserSummary | null>(null);
	let searchError = $state('');
	let confirmOpen = $state(false);
	let confirmApproved = false;
	let roleStatus = $state('');
	let mutation = $state<'role' | 'delete' | null>(null);
	let selectionVersion = 0;
	let deleteConfirmOpen = $state(false);
	let deleteTarget = $state<AdminUserSummary | null>(null);
	let deleteStatus = $state('');
	let deleteUncertain = $state(false);
	let deleteApproved = false;
	let deleteFormElement: HTMLFormElement | undefined = $state();
	const emptyDeletionForm = { userId: '' };
	let mayDelete = $derived(!!user && canDeleteUser(
		authService.user?.role ?? UserRole.USER, user.role, user.id === authService.user?.id
	));
	let roleFormElement: HTMLFormElement | undefined = $state();
	const emptyRoleForm: RoleChangeForm = {
		userId: '',
		fromRole: UserRole.USER,
		toRole: 'suspended_user',
		reason: ''
	};
	let choices = $derived(
		user
			? allowedRoleTargets(
					authService.user?.role ?? UserRole.USER,
					user.role,
					user.id === authService.user?.id
				)
			: []
	);

	function displayApiError(cause: unknown): string {
		if (cause instanceof ConnectError) {
			if (cause.code === Code.NotFound) return 'No account was found with that email address.';
			if (cause.code === Code.PermissionDenied)
				return 'You no longer have permission to manage user roles. Refresh your session or contact a super admin.';
			if (cause.code === Code.InvalidArgument)
				return 'The request was rejected. Check the fields and try again.';
			if (cause.code === Code.FailedPrecondition)
				return 'That account already has the selected role. Search again to refresh its details.';
		}
		return 'We could not complete that request. Check your connection and try again.';
	}

	function resetRole(target: AdminUserSummary | null) {
		selectionVersion++;
		deleteConfirmOpen = false;
		deleteTarget = null;
		deleteApproved = false;
		deleteUncertain = false;
		user = target;
		roleStatus = '';
		confirmOpen = false;
		confirmApproved = false;
		const available = target
			? allowedRoleTargets(
					authService.user?.role ?? UserRole.USER,
					target.role,
					target.id === authService.user?.id
				)
			: [];
		const next: RoleChangeForm = target
			? {
					userId: target.id,
					fromRole: target.role,
					toRole: available[0] ?? 'user',
					reason: ''
				}
			: emptyRoleForm;
		role.reset({ data: next, newState: next });
		return next;
	}

	async function refreshAdminAccess() {
		try {
			await authService.refresh();
			if (
				authService.user?.role !== UserRole.ADMIN &&
				authService.user?.role !== UserRole.SUPER_ADMIN
			) {
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
			if (!form.valid || mutation) return;
			resetRole(null);
			const version = selectionVersion;
			searchError = '';
			deleteStatus = '';
			try {
				const found = await adminService.getUserByEmail(form.data.email);
				if (version !== selectionVersion || mutation) return;
				resetRole(found);
			} catch (cause) {
				if (version !== selectionVersion || mutation) return;
				resetRole(null);
				searchError = displayApiError(cause);
				if (cause instanceof ConnectError && cause.code === Code.PermissionDenied)
					void refreshAdminAccess();
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
			if (!form.valid || mutation || $searching || user?.id !== form.data.userId) return;
			if (!confirmApproved) {
				confirmOpen = true;
				return;
			}
			confirmApproved = false;
			mutation = 'role';
			selectionVersion++;
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
						roleStatus =
							'This user’s role changed while you were reviewing it. Review the updated account and confirm again.';
					} catch (refreshCause) {
						roleStatus = displayApiError(refreshCause);
					}
				} else {
					setMessage(form, displayApiError(cause));
					if (cause instanceof ConnectError && cause.code === Code.PermissionDenied)
						void refreshAdminAccess();
				}
			} finally {
				mutation = null;
			}
		}
	});

	const deletion = superForm(defaults(emptyDeletionForm, zod4(userDeletionSchema)), {
		SPA: true,
		validators: zod4Client(userDeletionSchema),
		multipleSubmits: 'prevent',
		resetForm: false,
		async onUpdate({ form }) {
			const target = deleteTarget;
			if (!form.valid || !deleteApproved || mutation || $searching || !mayDelete ||
				!target || user?.id !== target.id || form.data.userId !== target.id) return;
			deleteApproved = false;
			mutation = 'delete';
			selectionVersion++;
			deleteStatus = '';
			try {
				await adminService.deleteUser(target.id);
				resetRole(null);
				deleteStatus = `Account ${target.email} was deleted. Retained contributions remain.`;
			} catch (cause) {
				deleteConfirmOpen = false;
				if (cause instanceof ConnectError && [Code.FailedPrecondition, Code.PermissionDenied, Code.NotFound].includes(cause.code)) {
					deleteStatus = cause.rawMessage;
				} else {
					deleteUncertain = true;
					const reason = cause instanceof ConnectError && cause.code === Code.Unavailable ? `${cause.rawMessage} ` : '';
					deleteStatus = `${reason}Deletion could not be confirmed for account ${target.id}. Search again to verify its current state before making another deletion request.`;
				}
			} finally {
				mutation = null;
			}
		}
	});
	const { form: deleteForm, enhance: enhanceDeletion } = deletion;

	function openDeletion() {
		if (!user || !mayDelete || mutation || $searching || deleteUncertain) return;
		deleteTarget = { ...user };
		const data = { userId: user.id };
		deletion.reset({ data, newState: data });
		deleteApproved = false;
		deleteConfirmOpen = true;
	}

	function confirmDeletion() {
		if (!deleteTarget || deleteTarget.id !== user?.id || mutation || !mayDelete) return;
		deleteApproved = true;
		deletion.submit(deleteFormElement);
	}

	const {
		form: searchForm,
		errors: searchErrors,
		submitting: searching,
		enhance: enhanceSearch
	} = search;
	const {
		form: roleForm,
		errors: roleErrors,
		constraints: roleConstraints,
		message: roleMessage,
		submitting: changingRole,
		enhance: enhanceRole
	} = role;

	function confirmChange() {
		confirmOpen = false;
		confirmApproved = true;
		role.submit(roleFormElement);
	}

	onMount(() => {
		void (async () => {
			if (!authService.isAuthenticated) {
				await goto('/login', { replaceState: true });
			} else if (
				authService.user?.role !== UserRole.ADMIN &&
				authService.user?.role !== UserRole.SUPER_ADMIN
			) {
				await goto('/', { replaceState: true });
			}
		})();
	});
</script>

<svelte:head><title>Manage users · Friend on Campus</title></svelte:head>

<section class="page-content admin-content" aria-labelledby="admin-title">
	<header class="page-heading">
		<div>
			<p class="eyebrow">Administration</p>
			<h1 id="admin-title">Manage users</h1>
			<p>
				Find an account by its exact email address, then review its current role before making a
				change.
			</p>
		</div>
	</header>

	<Card variant="outlined">
		<div class="panel-body">
			<form class="search-form" method="POST" use:enhanceSearch>
				<TextFieldOutlined
					label="User email address"
					type="email"
					disabled={!!mutation}
					name="email"
					autocomplete="email"
					required
					bind:value={$searchForm.email}
					error={!!$searchErrors.email}
					aria-invalid={$searchErrors.email ? 'true' : undefined}
					aria-describedby={$searchErrors.email ? 'admin-email-error' : undefined}
				/>
				{#if $searchErrors.email}<small class="field-error" id="admin-email-error"
						>{$searchErrors.email[0]}</small
					>{/if}
				<div class="form-actions">
					<Button type="submit" disabled={$searching || !!mutation || !!$searchErrors.email}
						>{$searching ? 'Searching…' : 'Find user'}</Button
					>
				</div>
			</form>
			{#if searchError}<p class="notice error" role="alert">{searchError}</p>{/if}
		</div>
	</Card>
	{#if deleteStatus}<p class="notice" role="status" aria-live="polite">{deleteStatus}</p>{/if}

	{#if user}
		<Card variant="outlined">
			<div class="panel-body">
				<div class="user-summary">
					<div>
						<p class="eyebrow">Account found</p>
						<h2>{user.displayName}</h2>
						<p>{user.email}</p>
						<small>Account ID: {user.id}</small>
					</div>
					<span class="role-chip">{roleLabel(user.role)}</span>
				</div>
				{#if choices.length === 0}
					<p class="notice" role="status">
						{user.id === authService.user?.id
							? 'You cannot change your own role.'
							: user.role === UserRole.SUPER_ADMIN
								? 'A super admin role cannot be changed here.'
								: 'No role changes are available for this account.'}
					</p>
				{:else}
					{#if roleStatus}<p class="notice" role="status">{roleStatus}</p>{/if}
					{#if $roleMessage}<p class="notice" role="status" aria-live="polite">
							{$roleMessage}
						</p>{/if}
					<form class="data-form" method="POST" use:enhanceRole bind:this={roleFormElement}>
						<SelectOutlined
							label="New role"
							disabled={!!mutation || $searching}
							name="toRole"
							aria-invalid={$roleErrors.toRole ? 'true' : undefined}
							aria-describedby={$roleErrors.toRole ? 'admin-role-error' : undefined}
							bind:value={$roleForm.toRole}
							options={choices.map((choice) => ({
								value: choice,
								text: roleLabel(roleValue(choice))
							}))}
						/>
						{#if $roleErrors.toRole}<small id="admin-role-error" class="field-error"
								>{$roleErrors.toRole[0]}</small
							>{/if}
						<div class="field">
							<TextFieldOutlinedMultiline
								label="Reason · required for suspending or reinstating"
								disabled={!!mutation || $searching}
								name="reason"
								rows={3}
								bind:value={$roleForm.reason}
								maxlength={Number($roleConstraints.reason?.maxlength ?? 2000)}
								error={!!$roleErrors.reason}
								aria-invalid={$roleErrors.reason ? 'true' : undefined}
								aria-describedby={$roleErrors.reason ? 'admin-reason-error' : undefined}
							/><small class="field-meta">{Array.from($roleForm.reason).length}/2,000</small
							>{#if $roleErrors.reason}<small class="field-error" id="admin-reason-error"
									>{$roleErrors.reason[0]}</small
								>{/if}
						</div>
						<div class="form-actions">
							<Button
								type="submit"
								disabled={!!mutation || $searching || $changingRole ||
									$roleForm.toRole ===
										(user.role === UserRole.ADMIN
											? 'admin'
											: user.role === UserRole.USER
												? 'user'
												: 'suspended_user') ||
									!!$roleErrors.toRole}>{$changingRole ? 'Updating…' : 'Review role change'}</Button
							>
						</div>
					</form>
				{/if}
				{#if mayDelete}
					<div class="delete-action">
						<Button type="button" disabled={!!mutation || $searching || deleteUncertain} onclick={openDeletion}>
							{mutation === 'delete' ? 'Deleting…' : 'Delete account'}
						</Button>
					</div>
				{/if}
			</div>
		</Card>
	{/if}
</section>

<ConfirmationDialog bind:open={confirmOpen} headline="Confirm role change" confirmLabel="Confirm role change" pending={$changingRole} pendingLabel="Updating…" onconfirm={confirmChange}>
	{#if user}
		<p>
			Change <strong>{user.displayName}</strong> ({user.email}) from
			<strong>{roleLabel(user.role)}</strong>
			to <strong>{roleLabel(roleValue($roleForm.toRole))}</strong>?
		</p>
		{#if $roleForm.reason}<p>Reason: {$roleForm.reason}</p>{/if}
	{/if}
</ConfirmationDialog>

<form hidden method="POST" use:enhanceDeletion bind:this={deleteFormElement}>
	<input type="hidden" name="userId" bind:value={$deleteForm.userId} />
</form>

<ConfirmationDialog bind:open={deleteConfirmOpen} headline="Permanently delete account?"
	confirmLabel="Delete account" destructive pending={mutation === 'delete'} pendingLabel="Deleting…"
	onconfirm={confirmDeletion}>
	{#if deleteTarget}
		<p>Delete <strong>{deleteTarget.displayName}</strong> ({deleteTarget.email}) permanently?</p>
		<p>Account ID: {deleteTarget.id}</p>
		<p>Account and sign-in data will be removed. Reviews and attached review photos remain as contributions
			from a deleted account. Profile pictures are account data and are not retained as contributions.</p>
		<p>This cannot be undone. The email address may register again as a new account with a new identity.</p>
	{/if}
</ConfirmationDialog>

<style>
	.delete-action {
		margin-top: 1.5rem;
		--m3c-primary: var(--m3c-error);
		--m3c-on-primary: var(--m3c-on-error);
	}
</style>
