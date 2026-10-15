<script lang="ts">
 import { untrack } from 'svelte';
 import { goto } from '$app/navigation';
 import { Button, Card, Checkbox, SelectOutlined, TextFieldOutlined, TextFieldOutlinedMultiline } from 'm3-svelte';
 import { defaults, superForm } from 'sveltekit-superforms';
 import { zod4, zod4Client } from 'sveltekit-superforms/adapters';
 import type { Location, Building, Category } from '$lib/gen/supplier/location/v1/shared_types_pb';
 import { locationAdminApi, locationApi } from '$lib/services';
 import { changedLocationPaths, locationSchema, locationForm, locationInput, type LocationForm } from '$lib/services/location-admin/locationSchema';
 import { adminError } from '$lib/services/location-admin/errors';
 import { Code, ConnectError } from '@connectrpc/connect';
 import { CreationAttempt } from '$lib/services/location-admin/retry';
 import type { LocationValues } from '$lib/services/location-admin/adminApi';
 import ConfirmDialog from './ConfirmDialog.svelte';
 let { location, buildings, categories, onsaved }: { location?: Location; buildings: Building[]; categories: Category[]; onsaved?: (value: Location) => void } = $props();
 let baseline = $state<LocationForm>(locationForm(untrack(() => location)));
 let revision = $state(untrack(() => location?.revision ?? 0n));
 let latest = $state<Location | null>(null);
 let error = $state(''), success = $state('');
 let conflict = $state(false), retryPending = $state(false), restartOpen = $state(false);
 const attempt = new CreationAttempt<LocationValues>();
 const initial = locationForm(untrack(() => location));
 const editor = superForm(defaults(initial, zod4(locationSchema)), {
  SPA: true, dataType: 'json', validators: zod4Client(locationSchema), validationMethod: 'onblur',
  multipleSubmits: 'prevent', resetForm: false, taintedMessage: true,
  async onUpdate({ form: validated }) {
   if (!validated.valid || conflict) return;
   error = ''; success = '';
   try {
    const input = locationInput(validated.data);
    const paths = changedLocationPaths(baseline, validated.data);
    if (location && !paths.length) { success = 'No changes to save.'; return; }
    let saved: Location;
    if (location) saved = await locationAdminApi.update(location.id, revision, input, paths);
    else { const retry = attempt.prepare(input); retryPending = true; saved = await locationAdminApi.create(retry.key, retry.payload); }
    attempt.clear(); retryPending = false;
    baseline = locationForm(saved); revision = saved.revision;
    editor.reset({ data: baseline, newState: baseline }); validated.data = baseline;
    success = 'Location saved.'; onsaved?.(saved);
    if (!location) await goto(`/admin/suppliers/${saved.id}`);
   } catch (cause) {
    if (cause instanceof ConnectError && cause.code === Code.Aborted) conflict = true;
    error = cause instanceof Error && cause.message.startsWith('A creation may') ? cause.message : adminError(cause);
   }
  }
 });
 const { form, errors, submitting, enhance, isTainted, tainted } = editor;
 function adoptLatest() {
  if (!latest) return;
  // Keep the draft. Only untouched fields advance to the latest server values.
  const server = locationForm(latest), draft = $form;
  const merged = { ...server };
  for (const key of Object.keys(draft) as (keyof LocationForm)[]) {
   if (JSON.stringify(draft[key]) !== JSON.stringify(baseline[key])) Object.assign(merged, { [key]: draft[key] });
  }
  baseline = server; revision = latest.revision;
  editor.reset({ data: merged, newState: server });
  form.set(merged, { taint: true });
  latest = null; conflict = false; error = ''; success = 'Latest version loaded. Your edited fields were kept. Review the form before saving.';
 }
 async function loadLatest() {
  if (!location) return;
  try { latest = await locationApi.getLocation(location.id); }
  catch (cause) { error = adminError(cause); }
 }
 function restart() { attempt.clear(); retryPending = false; restartOpen = false; error = ''; }
 const buildingOptions = $derived([{ value: '', text: 'Select a building' }, ...buildings.map((b) => ({ value: b.id, text: b.name }))]);
</script>
<Card variant="outlined"><div class="panel-body">
 <h2>{location ? 'Location details' : 'New location'}</h2>
 {#if error}<p class="notice error" role="alert">{error}</p>{/if}
 {#if success}<p class="notice" role="status">{success}</p>{/if}
 {#if conflict}
  <div class="data-form"><p class="notice" role="alert">Saving is paused because this record changed. Your draft is preserved.</p><div class="form-actions"><Button variant="outlined" onclick={loadLatest}>Load latest version</Button></div>
  {#if latest}<div class="notice"><p>Latest version: <strong>{latest.name}</strong>, revision {String(latest.revision)}.</p><p>Continue to merge untouched fields from the server and keep your edited fields. Review before saving.</p><Button variant="outlined" onclick={adoptLatest}>Reconcile with my draft</Button></div>{/if}</div>
 {/if}
 <form class="data-form" method="POST" use:enhance>
  <fieldset class="form-fieldset form-grid" disabled={$submitting}>
   <div class="field wide"><TextFieldOutlined label="Location name" name="name" required bind:value={$form.name} error={!!$errors.name} aria-describedby="location-name-error" />{#if $errors.name}<small id="location-name-error" class="field-error">{$errors.name[0]}</small>{/if}</div>
   <div class="field"><SelectOutlined label="Classification" value={$form.isSupplier ? 'supplier' : 'facility'} options={[{ value: 'supplier', text: 'Supplier' }, { value: 'facility', text: 'Facility' }]} onchange={(e) => { $form.isSupplier = e.currentTarget.value === 'supplier'; if (!$form.isSupplier) $form.categoryIds = []; }} /></div>
   <div class="field"><SelectOutlined label="Building" name="buildingId" options={buildingOptions} bind:value={$form.buildingId} required aria-invalid={$errors.buildingId ? 'true' : undefined} aria-describedby="location-building-error" />{#if $errors.buildingId}<small id="location-building-error" class="field-error">{$errors.buildingId[0]}</small>{/if}</div>
   {#if $form.isSupplier}<fieldset class="field wide form-fieldset"><legend>Categories · select at least one</legend><div class="category-options">{#each categories as category}<label class="checkbox-label"><Checkbox><input type="checkbox" value={category.id} bind:group={$form.categoryIds} aria-describedby="location-categories-error" /></Checkbox>{category.name}</label>{/each}</div>{#if $errors.categoryIds}<small id="location-categories-error" class="field-error">{typeof $errors.categoryIds === 'string' ? $errors.categoryIds : $errors.categoryIds._errors?.[0]}</small>{/if}</fieldset>{/if}
   <div class="field"><TextFieldOutlined label="Floor · optional" name="floor" bind:value={$form.floor} error={!!$errors.floor} aria-describedby="location-floor-error" />{#if $errors.floor}<small id="location-floor-error" class="field-error">{$errors.floor[0]}</small>{/if}</div>
   <div class="field"><TextFieldOutlined label="Contact · optional" name="contact" bind:value={$form.contact} error={!!$errors.contact} aria-describedby="location-contact-error" />{#if $errors.contact}<small id="location-contact-error" class="field-error">{$errors.contact[0]}</small>{/if}</div>
   <div class="field"><TextFieldOutlined label="Latitude (−90 to 90)" name="latitude" inputmode="decimal" required bind:value={$form.latitude} error={!!$errors.latitude} aria-describedby="location-latitude-error" />{#if $errors.latitude}<small id="location-latitude-error" class="field-error">{$errors.latitude[0]}</small>{/if}</div>
   <div class="field"><TextFieldOutlined label="Longitude (−180 to 180)" name="longitude" inputmode="decimal" required bind:value={$form.longitude} error={!!$errors.longitude} aria-describedby="location-longitude-error" />{#if $errors.longitude}<small id="location-longitude-error" class="field-error">{$errors.longitude[0]}</small>{/if}</div>
   <p class="secondary-text wide" id="opening-hours-help">Opening hours use Singapore time (24-hour). Leave both empty if unknown. A closing time before opening means overnight.</p>
   <div class="field"><TextFieldOutlined label="Opens at · optional" name="opensAt" type="time" bind:value={$form.opensAt} error={!!$errors.opensAt} aria-describedby="opening-hours-help location-opens-error" />{#if $errors.opensAt}<small id="location-opens-error" class="field-error">{$errors.opensAt[0]}</small>{/if}</div>
   <div class="field"><TextFieldOutlined label="Closes at · optional" name="closesAt" type="time" bind:value={$form.closesAt} error={!!$errors.closesAt} aria-describedby="opening-hours-help location-closes-error" />{#if $errors.closesAt}<small id="location-closes-error" class="field-error">{$errors.closesAt[0]}</small>{/if}</div>
   <div class="field wide"><TextFieldOutlinedMultiline label="Directions and purchase instructions · optional" name="details" rows={4} bind:value={$form.details} error={!!$errors.details} aria-describedby="location-details-error" />{#if $errors.details}<small id="location-details-error" class="field-error">{$errors.details[0]}</small>{/if}</div>
  </fieldset>
  <div class="form-actions"><Button type="button" variant="text" href="/admin/suppliers">Back to locations</Button><Button type="submit" disabled={$submitting || conflict || (!!location && !isTainted($tainted))}>{$submitting ? 'Saving…' : location ? 'Save changes' : 'Create location'}</Button></div>
 </form>
 {#if retryPending}<p class="notice" role="status">A creation has been attempted. Keep these details unchanged to retry safely. Check the location list before starting another creation.</p><Button variant="text" onclick={() => restartOpen = true}>Start a new creation attempt</Button>{/if}
</div></Card>
<ConfirmDialog bind:open={restartOpen} title="Start a new creation?" label="Start new attempt" onconfirm={restart}><p>The previous attempt may already have created a location. Check the list first to avoid a duplicate. This will discard its retry key.</p></ConfirmDialog>
