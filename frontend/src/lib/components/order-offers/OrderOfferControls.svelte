<script lang="ts">
 import { onDestroy } from 'svelte';
 import { Code, ConnectError } from '@connectrpc/connect';
 import { timestampDate } from '@bufbuild/protobuf/wkt';
 import { AssignmentAttemptStatus, OfferStatus, OrderStatus, type Offer, type Order } from '$lib/gen/order/v1/shared_types_pb';
 import { OrderActionRejected, type OrderOffersApi } from '$lib/services/order-offers';
 import { clearOfferAction, loadOfferAction, saveOfferAction, isProposalRetryExpired, type OfferAction } from '$lib/services/order-offers/retry';
 import { describeOrderError } from '$lib/services/order/errors';
 let { order, refresh, api, callerId, suspended }: {
  order: Order; refresh: () => Promise<void>; api: OrderOffersApi; callerId: string; suspended: boolean
 } = $props();
 let offers = $state<Offer[]>([]), amount = $state(''), action = $state<OfferAction | null>(null);
 let message = $state(''), error = $state(''), listError = $state('');
 let busy = $state(false), storageReady = $state(false), now = $state(Date.now());
 let epoch = 0, offerRead = 0, loadedScope = '';
 const requester = $derived(order.requesterId === callerId);
 const beforeDeadline = $derived(Boolean(order.expiresAt && timestampDate(order.expiresAt).getTime() > now));
 const canStart = $derived(Boolean(callerId && !suspended && !busy && storageReady && !action && order.status === OrderStatus.AWAITING_COURIER && beforeDeadline));
 const expiredProposal = $derived(Boolean(action && isProposalRetryExpired(action, now)));
 const resolved = $derived(action?.outcome === 'succeeded' || action?.outcome === 'rejected' || expiredProposal);
 function outcomeMessage(value: OfferAction): string {
  switch (value.outcome) {
   case 'pending': return 'Acceptance is pending. No assignment success has been confirmed.';
   case 'compensating': return 'Credit recovery is pending. Refresh offers for a read-only update. Repeating counteroffer acceptance can start a new financial attempt after recovery.';
   case 'succeeded': return 'This earlier attempt succeeded. Check the refreshed order for its CURRENT assignment.';
   case 'rejected': return 'This attempt was rejected. A deliberate new action needs a new retry identity.';
   default: return 'The previous result is uncertain. Retry the same saved request, not a new action.';
  }
 }
 $effect(() => {
  const scope = `${callerId}:${order.id}`;
  if (scope === loadedScope) return;
  loadedScope = scope; epoch++; offerRead++;
  action = null; offers = []; amount = ''; message = ''; error = ''; listError = ''; storageReady = false; busy = false;
  if (!callerId) return;
  try {
   action = loadOfferAction(sessionStorage, callerId, order.id);
   amount = action?.credits ?? ''; message = action ? outcomeMessage(action) : ''; storageReady = true;
  } catch { error = 'Cannot safely recover stored retry data. No new mutation will be sent. Check My Orders before clearing browser storage.'; }
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
  if (isProposalRetryExpired(next)) { error = 'The proposal retry window has ended. Refresh offers and start a deliberate new action if needed.'; return; }
  const current = epoch, caller = callerId, id = order.id;
  busy = true; error = '';
  try {
   saveOfferAction(sessionStorage, caller, id, next); action = next;
   if (next.kind === 'proposal') {
    await api.makeOffer(id, BigInt(next.credits!), next.key);
    if (current !== epoch) return;
    clearOfferAction(sessionStorage, caller, id); action = null;
    message = 'Proposal recorded. This does not assign a courier or increase the reservation.';
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
    ? 'A competing update or assignment may be in progress. Refresh and retry the saved request.'
    : describeOrderError(cause, 'complete this action') + ' Keep the saved identity for an uncertain result.'; }
  } finally {
   if (current === epoch) {
    try { await refresh(); await loadOffers(); }
    catch { if (current === epoch) error += ' The current view could not refresh. The saved action result is unchanged.'; }
    finally { if (current === epoch) busy = false; }
   }
  }
 }
 function retrySaved() {
  if (!action) return;
  if (action.kind === 'counteroffer' && !window.confirm('Repeating acceptance may start a new financial attempt after an earlier refusal. Refresh offers instead for a read-only update. Repeat acceptance?')) return;
  void perform(action);
 }
 function propose(event: SubmitEvent) {
  event.preventDefault();
  if (!canStart || requester) return;
  if (!/^[1-9][0-9]{0,18}$/.test(amount) || BigInt(amount) > 9223372036854775807n || BigInt(amount) <= order.rewardCredits) {
   error = 'Enter whole credits above the current reward and within the supported range.'; return;
  }
  void perform({ kind: 'proposal', key: crypto.randomUUID(), credits: amount, dispatchedAt: Date.now(), outcome: 'uncertain' });
 }
 function newIntent() {
  if (!resolved || busy || suspended) return;
  try { clearOfferAction(sessionStorage, callerId, order.id); action = null; storageReady = true; message = 'A new action will use a new retry identity.'; error = ''; }
  catch { error = 'Cannot clear confirmed retry data. No new action was sent.'; }
 }
 function offerLabel(status: OfferStatus): string {
  const labels: Partial<Record<OfferStatus, string>> = {[OfferStatus.OPEN]:'Open',[OfferStatus.ACCEPTING]:'Accepting',[OfferStatus.ACCEPTED]:'Accepted',[OfferStatus.VOID]:'Historical / void'};
  return labels[status] ?? 'Unknown';
 }
</script>

<section aria-label="Acceptance and offers" class="offers">
 <h2>Acceptance and offers</h2>
 {#if suspended}<p>Your account can read its history but cannot accept or make offers.</p>{/if}
 {#if message}<p role="status">{message}</p>{/if}
 {#if error}<p role="alert">{error}</p>{/if}
 {#if expiredProposal}<p>The saved proposal is past its 24-hour retry window, or its dispatch time is unknown. Refresh offers before deciding whether to submit a new proposal. A new proposal may replace your existing open offer.</p>{/if}
 {#if action}
  {#if resolved}<button type="button" disabled={busy || suspended} onclick={newIntent}>Start a deliberate new action</button>
  {:else}<button type="button" disabled={busy || suspended || !storageReady} onclick={retrySaved}>{busy ? 'Sending…' : action.kind === 'counteroffer' ? 'Repeat saved acceptance (may start a new financial attempt)' : 'Retry the same saved request'}</button>{/if}
 {/if}
 {#if !requester}
  <button type="button" disabled={!canStart} onclick={() => void perform({ kind:'accept', key:crypto.randomUUID(), outcome:'uncertain' })}>Accept current reward ({order.rewardCredits.toString()} credits)</button>
  <form onsubmit={propose}>
   <label>Counteroffer credits <input aria-label="Counteroffer credits" inputmode="numeric" bind:value={amount} disabled={!canStart} autocomplete="off" /></label>
   <button type="submit" disabled={!canStart}>Submit / replace my open offer</button>
  </form>
  <p>Offers must exceed the current reward. Accepted and void offers remain historical.</p>
 {/if}
 <button type="button" disabled={busy} onclick={() => void loadOffers()}>Refresh offers</button>
 {#if listError}<p role="alert">{listError}</p>{/if}
 {#if !offers.length}<p>No visible offers.</p>{:else}
  <ul>{#each offers as offer (offer.id)}
   <li><span>{offer.credits.toString()} credits · {offerLabel(offer.status)} · courier {offer.courierId}</span>
    {#if requester && offer.status === OfferStatus.OPEN}
     <button type="button" disabled={!canStart} onclick={() => void perform({kind:'counteroffer',key:crypto.randomUUID(),offerId:offer.id,outcome:'uncertain'})}>Accept {offer.credits.toString()}-credit offer</button>
    {/if}
   </li>
  {/each}</ul>
 {/if}
</section>
<style>
 .offers { display:grid; gap:.75rem; min-width:0; } h2,p { margin:0; }
 form,li { display:flex; gap:.5rem; flex-wrap:wrap; align-items:center; }
 input { width:10rem; max-width:100%; } button { padding:.5rem .75rem; white-space:normal; }
 span { overflow-wrap:anywhere; } ul { padding-left:1rem; display:grid; gap:.5rem; }
</style>
