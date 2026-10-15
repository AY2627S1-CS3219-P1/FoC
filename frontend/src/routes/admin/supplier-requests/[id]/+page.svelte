<script lang="ts">
 import { page } from '$app/state';
 import { Button, Card, LoadingIndicator, TextFieldOutlinedMultiline } from 'm3-svelte';
 import { defaults, superForm } from 'sveltekit-superforms';
 import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
 import { additionRequestApi, locationApi } from '$lib/services';
 import { LocationAdditionRequestStatus as Status, type LocationAdditionRequest } from '$lib/gen/supplier/location/v1/addition_request_pb';
 import type { Building, Category } from '$lib/gen/supplier/location/v1/shared_types_pb';
 import { adminError, isConflict } from '$lib/services/location-admin/errors';
 import { formatTimestamp, requestStatusLabel } from '$lib/services/location-admin/format';
 import { formatOpeningHours } from '$lib/services/location/format';
 import { rejectionSchema } from '$lib/services/addition-request/reviewSchema';
 import ConfirmDialog from '$lib/components/admin/ConfirmDialog.svelte';
 let request = $state<LocationAdditionRequest | null>(null), loading = $state(true), error = $state('');
 let buildings = $state<Building[]>([]), categories = $state<Category[]>([]), referenceError = $state('');
 let reload = $state(0), referenceReload = $state(0), busy = $state(false), confirmOpen = $state(false);
 let action = $state<'approve' | 'reject'>('approve'), note = $state(''), notice = $state('');
 let reviewBlocked = $state(false);
 $effect(() => {
  const id = page.params.id ?? ''; void reload;
  let stale = false; loading = true; error = ''; request = null;
  additionRequestApi.get(id).then((value) => { if (!stale) { request = value; reviewBlocked = false; } })
   .catch((cause) => { if (!stale) error = adminError(cause, 'request'); })
   .finally(() => { if (!stale) loading = false; });
  return () => { stale = true; };
 });
 $effect(() => {
  void referenceReload; let stale = false; referenceError = '';
  Promise.all([locationApi.listBuildings(), locationApi.listCategories()]).then(([b, c]) => { if (!stale) { buildings = b; categories = c; } })
   .catch(() => { if (!stale) referenceError = 'Building/category names are unavailable. Reference IDs are shown instead.'; });
  return () => { stale = true; };
 });
 const formId = $props.id();
 const rejection = superForm(defaults({ reason: '' }, zod4(rejectionSchema)), {
  id: formId, SPA: true, validators: zod4Client(rejectionSchema), validationMethod: 'onblur', multipleSubmits: 'prevent', resetForm: false, taintedMessage: true,
  async onUpdate({ form }) { if (form.valid) { note = form.data.reason; await review('reject'); } }
 });
 const { form, errors, enhance, submitting } = rejection;
 async function review(next: 'approve' | 'reject') {
  if (!request || busy || reviewBlocked) return;
  busy = true; error = ''; notice = '';
  try {
   const latest = await additionRequestApi.get(request.id);
   if (latest.revision !== request.revision || latest.status !== Status.PENDING) {
    request = latest; notice = 'This request changed. Review its latest details before deciding.'; return;
   }
   action = next; confirmOpen = true;
  } catch (cause) { error = adminError(cause, 'request'); }
  finally { busy = false; }
 }
 async function decide() {
  if (!request || busy) return;
  busy = true; error = ''; reviewBlocked = true;
  try {
   request = action === 'approve' ? await additionRequestApi.approve(request.id) : await additionRequestApi.reject(request.id, note);
   rejection.reset(); notice = action === 'approve' ? 'Request approved. The location is now available.' : 'Request rejected.';
  } catch (cause) {
   error = adminError(cause, 'request');
   // Re-read after uncertain results as well: a network failure may follow a committed decision.
   try { request = await additionRequestApi.get(request.id); reviewBlocked = false; }
   catch { notice = 'Could not refresh the request. Reload before trying another decision.'; }
   if (isConflict(cause)) notice = 'The request has been refreshed. Review its current status.';
  } finally { busy = false; confirmOpen = false; }
 }
</script>
<svelte:head><title>Review addition request · Friend on Campus</title></svelte:head>
<section class="page-content admin-content">
 <div><Button variant="text" href="/admin/supplier-requests">← Addition requests</Button></div>
 {#if error}<p class="notice error" role="alert">{error}</p>{/if}
 {#if notice}<p class="notice" role="status">{notice}</p>{/if}
 {#if loading}<div class="loading-state" role="status"><LoadingIndicator aria-label="Loading proposal" /><p>Loading proposal…</p></div>
 {:else if !request}<Button onclick={() => reload++}>Retry request</Button>
 {:else}
  <header class="page-heading"><div><p class="eyebrow">Addition request · {requestStatusLabel(request.status)}</p><h1>{request.proposal?.name ?? 'Unnamed proposal'}</h1><p>Submitted {formatTimestamp(request.createdAt)} (Singapore time)</p></div><Button variant="outlined" disabled={busy} onclick={() => reload++}>Reload request</Button></header>
  {#if referenceError}<p class="notice" role="status">{referenceError}</p><Button variant="text" onclick={() => referenceReload++}>Retry reference names</Button>{/if}
  <Card variant="outlined"><div class="panel-body"><h2>Proposed location</h2>
   {#if request.proposal}<dl class="admin-facts">
    <div><dt>Classification</dt><dd>{request.proposal.isSupplier ? 'Supplier' : 'Facility'}</dd></div>
    <div><dt>Building</dt><dd>{buildings.find((b) => b.id === request!.proposal!.buildingId)?.name ?? request.proposal.buildingId}</dd></div>
    <div><dt>Categories</dt><dd>{request.proposal.categoryIds.map((id) => categories.find((c) => c.id === id)?.name ?? id).join(', ') || 'None'}</dd></div>
    <div><dt>Floor</dt><dd>{request.proposal.floor || '—'}</dd></div>
    <div><dt>Coordinates</dt><dd>{request.proposal.coordinates ? `${request.proposal.coordinates.latitude}, ${request.proposal.coordinates.longitude}` : 'Not supplied'}</dd></div>
    <div><dt>Opening hours</dt><dd>{formatOpeningHours(request.proposal.opensAt, request.proposal.closesAt)} (Singapore time)</dd></div>
    <div><dt>Contact</dt><dd>{request.proposal.contact || '—'}</dd></div>
    <div><dt>Details</dt><dd>{request.proposal.details || '—'}</dd></div>
   </dl>{:else}<p class="notice error" role="alert">The response did not include proposal details.</p>{/if}
  </div></Card>
  <Card variant="outlined"><div class="panel-body"><h2>Request history</h2><dl class="admin-facts">
   <div><dt>Submitted by</dt><dd>{request.submittedBy || 'Unavailable'}</dd></div>
   <div><dt>Last updated</dt><dd>{formatTimestamp(request.updatedAt)} (SGT)</dd></div>
   <div><dt>Reviewed by</dt><dd>{request.reviewedBy || '—'}</dd></div>
   <div><dt>Reviewed at</dt><dd>{formatTimestamp(request.reviewedAt)} (SGT)</dd></div>
   <div><dt>Review note</dt><dd>{request.reviewNote || '—'}</dd></div>
  </dl>{#if request.resultingLocationId}<Button href={`/admin/suppliers/${request.resultingLocationId}`}>Manage resulting location</Button>{/if}</div></Card>
  {#if request.status === Status.PENDING && request.proposal}
   <Card variant="outlined"><div class="panel-body"><h2>Review decision</h2><p>Approval publishes the proposed location. Rejection requires a reason. Decisions are final.</p>
    <form class="data-form" method="POST" use:enhance><div class="field"><TextFieldOutlinedMultiline label="Rejection reason" name="reason" rows={3} bind:value={$form.reason} error={!!$errors.reason} aria-describedby="rejection-error" />{#if $errors.reason}<small id="rejection-error" class="field-error">{$errors.reason[0]}</small>{/if}</div>
     <div class="form-actions"><Button type="submit" variant="outlined" disabled={busy || $submitting || reviewBlocked}>Review rejection</Button><Button type="button" disabled={busy || reviewBlocked} onclick={() => review('approve')}>Review approval</Button></div>
    </form>
   </div></Card>
   {#if reviewBlocked}<Button disabled={busy} onclick={() => reload++}>Reload before continuing</Button>{/if}
  {/if}
 {/if}
</section>
<ConfirmDialog bind:open={confirmOpen} title={action === 'approve' ? 'Approve this request?' : 'Reject this request?'} label={action === 'approve' ? 'Approve and publish' : 'Reject request'} {busy} onconfirm={decide}><p>{action === 'approve' ? 'Publish' : 'Reject'} <strong>{request?.proposal?.name}</strong>? This decision is final.</p>{#if action === 'reject'}<p>Reason: {note}</p>{/if}</ConfirmDialog>
