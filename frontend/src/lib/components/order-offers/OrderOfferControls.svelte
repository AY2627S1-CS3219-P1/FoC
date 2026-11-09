<script lang="ts">
 import { onDestroy } from 'svelte';
 import { Code, ConnectError } from '@connectrpc/connect';
 import { timestampDate } from '@bufbuild/protobuf/wkt';
 import { Button, TextFieldOutlined } from 'm3-svelte';
 import { defaults, setError, superForm } from 'sveltekit-superforms';
 import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
 import { AssignmentAttemptStatus, OfferStatus, OrderStatus, type Offer, type Order } from '$lib/gen/order/v1/shared_types_pb';
 import { OrderActionRejected, type OrderOffersApi } from '$lib/services/order-offers';
 import { clearOfferAction, loadOfferAction, saveOfferAction, isProposalRetryExpired, type OfferAction } from '$lib/services/order-offers/retry';
 import { describeOrderError } from '$lib/services/order/errors';
 import { proposalSchema } from '$lib/services/order-offers/proposalSchema';
 let { order, refresh, api, callerId, suspended }: {
  order: Order; refresh: () => Promise<void>; api: OrderOffersApi; callerId: string; suspended: boolean
 } = $props();
 let offers = $state<Offer[]>([]), action = $state<OfferAction | null>(null);
 let message = $state(''), error = $state(''), listError = $state('');
 let busy = $state(false), storageReady = $state(false), now = $state(Date.now());
 let epoch = 0, offerRead = 0, loadedScope = '';
 const requester = $derived(order.requesterId === callerId);
 const beforeDeadline = $derived(Boolean(order.expiresAt && timestampDate(order.expiresAt).getTime() > now));
 const canStart = $derived(Boolean(callerId && !suspended && !busy && storageReady && !action && order.status === OrderStatus.AWAITING_COURIER && beforeDeadline));
 const expiredProposal = $derived(Boolean(action && isProposalRetryExpired(action, now)));
 const resolved = $derived(action?.outcome === 'succeeded' || action?.outcome === 'rejected' || expiredProposal);
 const schema = proposalSchema(() => order.rewardCredits);
 let proposalEpoch = -1;
 const { form, errors, enhance, reset } = superForm(defaults({ credits: '' }, zod4(schema)), {
  SPA: true, validators: zod4Client(schema), resetForm: false, multipleSubmits: 'prevent',
  onSubmit({ cancel }) {
   if (!canStart || requester) { cancel(); return; }
   proposalEpoch = epoch;
  },
  async onUpdate({ form: validated, cancel }) {
   const submittedEpoch = proposalEpoch;
   if (submittedEpoch !== epoch) { cancel(); return; }
   if (!validated.valid || !canStart || requester) return;
   // The reward can refresh between asynchronous validation and dispatch.
   if (BigInt(validated.data.credits) <= order.rewardCredits) {
    setError(validated, 'credits', 'Enter whole credits above the current reward and within the supported range.'); return;
   }
   await perform({ kind: 'proposal', key: crypto.randomUUID(), credits: validated.data.credits, dispatchedAt: Date.now(), outcome: 'uncertain' });
   if (submittedEpoch !== epoch) cancel();
  }
 });
 function outcomeMessage(value: OfferAction): string {
  switch (value.outcome) {
   case 'pending': return 'Still confirming your acceptance. Try again to check the result.';
   case 'compensating': return 'We could not complete acceptance yet. Refresh offers to check progress. Accepting again may reserve credits again.';
   case 'succeeded': return 'Your earlier acceptance was confirmed. Refresh the order to see the current courier.';
   case 'rejected': return 'This could not be completed. Choose "Start again" to try again.';
   default: return 'We could not confirm the result. Try again to check it.';
  }
 }
 $effect(() => {
  const scope = `${callerId}:${order.id}`;
  if (scope === loadedScope) return;
  loadedScope = scope; epoch++; offerRead++;
  action = null; offers = []; reset({ id: `proposal-${epoch}`, data: { credits: '' } }); message = ''; error = ''; listError = ''; storageReady = false; busy = false;
  if (!callerId) return;
  try {
   action = loadOfferAction(sessionStorage, callerId, order.id);
   reset({ id: `proposal-${epoch}`, data: { credits: action?.credits ?? '' } }); message = action ? outcomeMessage(action) : ''; storageReady = true;
  } catch { error = 'Could not restore your last action. Check My Orders before trying again.'; }
 });
 async function loadOffers() {
  const id = order.id, caller = callerId, read = ++offerRead;
  if (!caller) return;
  try {
   const result = await api.listOffers(id);
   if (read === offerRead && callerId === caller && order.id === id) { offers = result; listError = ''; }
  } catch (cause) { if (read === offerRead && callerId === caller && order.id === id) listError = describeOrderError(cause, 'load offers'); }
 }
 $effect(() => { const revision = order.revision; void revision; void loadOffers(); });
 $effect(() => { const timer = setInterval(() => { now = Date.now(); }, 1000); return () => clearInterval(timer); });
 onDestroy(() => { epoch++; offerRead++; });
 async function perform(next: OfferAction) {
  if (busy || suspended || !callerId || !storageReady) return;
  if (isProposalRetryExpired(next)) { error = 'This offer is too old to check again. Refresh offers before submitting a new one.'; return; }
  const current = epoch, caller = callerId, id = order.id;
  busy = true; error = '';
  try {
   saveOfferAction(sessionStorage, caller, id, next); action = next;
   if (next.kind === 'proposal') {
    await api.makeOffer(id, BigInt(next.credits!), next.key);
    if (current !== epoch) return;
    clearOfferAction(sessionStorage, caller, id); action = null;
    message = 'Offer submitted. The requester still needs to accept it.';
   } else {
    const result = next.kind === 'accept' ? await api.acceptOrder(id, next.key) : await api.acceptOffer(next.offerId!);
    if (current !== epoch) return;
    const status = result.attempt.status;
    const outcome = status === AssignmentAttemptStatus.SUCCEEDED ? 'succeeded' : status === AssignmentAttemptStatus.COMPENSATING ? 'compensating' : status === AssignmentAttemptStatus.REJECTED ? 'rejected' : 'pending';
    action = { ...next, outcome }; saveOfferAction(sessionStorage, caller, id, action); message = outcomeMessage(action);
   }
  } catch (cause) {
   if (current !== epoch) return;
   if (cause instanceof OrderActionRejected) {
    action = { ...next, outcome: 'rejected' };
    try { saveOfferAction(sessionStorage, caller, id, action); } catch { storageReady = false; }
    message = cause.message;
   } else { error = ConnectError.from(cause).code === Code.Aborted
    ? 'The order may have changed. Refresh, then try again.'
    : describeOrderError(cause, 'complete this action') + ' Try again to check the result.'; }
  } finally {
   if (current === epoch) {
    try { await refresh(); await loadOffers(); }
    catch { if (current === epoch) error += ' Could not update the order. Refresh to try again.'; }
    finally { if (current === epoch) busy = false; }
   }
  }
 }
 function retrySaved() {
  if (!action) return;
  if (action.kind === 'counteroffer' && !window.confirm('Accepting again may reserve credits again. To check the result without accepting again, choose "Refresh offers". Accept again?')) return;
  void perform(action);
 }
 function newIntent() {
  if (!resolved || busy || suspended) return;
  try { clearOfferAction(sessionStorage, callerId, order.id); action = null; storageReady = true; message = 'You can now try again.'; error = ''; }
  catch { error = 'Could not start again. Nothing was sent.'; }
 }
 function offerLabel(status: OfferStatus): string {
  const labels: Partial<Record<OfferStatus, string>> = {[OfferStatus.OPEN]:'Open',[OfferStatus.ACCEPTING]:'Accepting',[OfferStatus.ACCEPTED]:'Accepted',[OfferStatus.VOID]:'No longer active'};
  return labels[status] ?? 'Unknown';
 }
</script>

<section aria-label="Acceptance and offers" class="offers">
 <header class="offers-heading">
  <h2>Acceptance and offers</h2>
  <Button type="button" variant="text" disabled={busy} onclick={() => void loadOffers()}>Refresh offers</Button>
 </header>
 {#if suspended}<p>Your account is suspended. You can view history but cannot accept or make offers.</p>{/if}
 {#if message}<p role="status">{message}</p>{/if}
 {#if error}<p role="alert">{error}</p>{/if}
 {#if expiredProposal}<p>We cannot check this earlier offer again. Refresh offers before submitting another. A new offer may replace your open offer.</p>{/if}
 {#if action}
  {#if resolved}<Button type="button" variant="outlined" disabled={busy || suspended} onclick={newIntent}>Start again</Button>
  {:else}<Button type="button" variant="outlined" disabled={busy || suspended || !storageReady} onclick={retrySaved}>{busy ? 'Sending…' : action.kind === 'counteroffer' ? 'Accept again (may reserve credits again)' : 'Try again'}</Button>{/if}
 {/if}
 {#if !requester}
  <Button type="button" disabled={!canStart} onclick={() => void perform({ kind:'accept', key:crypto.randomUUID(), outcome:'uncertain' })}>Accept current reward ({order.rewardCredits.toString()} credits)</Button>
  <form method="POST" use:enhance class="proposal-form">
   <div class="field">
    <TextFieldOutlined label="Counteroffer credits" name="credits" aria-label="Counteroffer credits" inputmode="numeric" bind:value={$form.credits} disabled={!canStart} autocomplete="off" error={!!$errors.credits} aria-invalid={$errors.credits ? 'true' : undefined} aria-describedby={$errors.credits ? 'proposal-credits-help proposal-credits-error' : 'proposal-credits-help'} />
    <small id="proposal-credits-help" class="secondary-text">Offers must exceed the current reward.</small>
    {#if $errors.credits}<small id="proposal-credits-error" class="field-error" role="alert">{$errors.credits[0]}</small>{/if}
   </div>
   <Button type="submit" disabled={!canStart}>Submit or replace my offer</Button>
  </form>
 {/if}
 {#if listError}<p role="alert">{listError}</p>{/if}
 {#if !offers.length}<p>No offers to show.</p>{:else}
  <ul class="record-list">{#each offers as offer (offer.id)}
   <li class="record-row"><span>{offer.credits.toString()} credits · {offerLabel(offer.status)}</span>
    {#if requester && offer.status === OfferStatus.OPEN}
     <Button type="button" disabled={!canStart} onclick={() => void perform({kind:'counteroffer',key:crypto.randomUUID(),offerId:offer.id,outcome:'uncertain'})}>Accept {offer.credits.toString()}-credit offer</Button>
    {/if}
   </li>
  {/each}</ul>
 {/if}
</section>
<style>
 .offers { display: grid; gap: 1rem; min-width: 0; }
 h2, p { margin: 0; }
 h2 { font-size: 1.35rem; line-height: 1.3; }
 .offers-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: .5rem 1rem; }
 .offers > :global(button) { justify-self: start; }
 .proposal-form { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: .75rem; align-items: start; max-width: 36rem; min-width: 0; }
 .proposal-form > :global(button) { margin-top: .5rem; }
 .offers :global(button) { max-width: 100%; white-space: normal; height: auto; min-height: 2.5rem; padding-block: .5rem; }
 .field :global(.m3-container) { width: 100%; min-width: 0; }
 .record-row { gap: .75rem; }
 .record-row > span { min-width: 0; overflow-wrap: anywhere; }
 .record-row > :global(button) { flex-shrink: 0; }
 @media (max-width: 36rem) {
  .proposal-form { grid-template-columns: minmax(0, 1fr); }
  .proposal-form > :global(button) { margin-top: 0; justify-self: start; }
  .record-row > :global(button) { align-self: start; }
 }
</style>
