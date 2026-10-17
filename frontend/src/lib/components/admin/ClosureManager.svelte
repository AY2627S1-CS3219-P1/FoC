<script lang="ts">
  import { onMount } from "svelte";
  import {
    Button,
    Card,
    SelectOutlined,
    TextFieldOutlined,
    TextFieldOutlinedMultiline,
  } from "m3-svelte";
  import { defaults, superForm } from "sveltekit-superforms";
  import { zod4, zod4Client } from "sveltekit-superforms/adapters";
  import { Code, ConnectError } from "@connectrpc/connect";
  import {
    DisablementState as State,
    type Disablement,
  } from "$lib/gen/supplier/location/v1/shared_types_pb";
  import { disablementApi } from "$lib/services";
  import type { ClosurePage } from "$lib/services/disablement/disablementApi";
  import {
    closureSchema,
    closureForm,
    closureInput,
    changedClosurePaths,
    type ClosureForm,
  } from "$lib/services/disablement/closureSchema";
  import { CreationAttempt } from "$lib/services/location-admin/retry";
  import { adminError } from "$lib/services/location-admin/errors";
  import { formatTimestamp } from "$lib/services/location-admin/format";
  import Pagination from "./Pagination.svelte";
  import ConfirmDialog from "./ConfirmDialog.svelte";
  let {
    locationId,
    archived,
    onchange,
  }: { locationId: string; archived: boolean; onchange: (id: string) => void } =
    $props();
  let result = $state<ClosurePage | null>(null),
    loading = $state(true),
    loadError = $state("");
  let closureState = $state("0"),
    page = $state(1),
    reload = $state(0),
    busy = $state(false);
  let selected = $state<Disablement | null>(null),
    baseline = $state<ClosureForm>(closureForm());
  let error = $state(""),
    notice = $state(""),
    conflict = $state(false),
    retryPending = $state(false);
  let confirmation = $state<"end" | "cancel" | "restart" | "discard" | null>(
      null,
    ),
    confirmOpen = $state(false);
  let target = $state<Disablement | null>(null);
  let selectAfterDiscard: Disablement | null = null;
  const attempt = new CreationAttempt<ClosureForm>();
  const stateOptions = [
    { value: "0", text: "All closures" },
    { value: "1", text: "Scheduled" },
    { value: "2", text: "Active" },
    { value: "3", text: "Ended" },
    { value: "4", text: "Cancelled" },
  ];
  const stateLabel = (value: number) =>
    stateOptions.find((s) => Number(s.value) === value)?.text ?? "Unknown";
  $effect(() => {
    const id = locationId,
      filter = Number(closureState),
      number = page;
    void reload;
    let stale = false;
    loading = true;
    loadError = "";
    result = null;
    disablementApi
      .list(id, filter, number)
      .then((value) => {
        if (!stale) result = value;
      })
      .catch((cause) => {
        if (!stale) loadError = adminError(cause, "closure");
      })
      .finally(() => {
        if (!stale) loading = false;
      });
    return () => {
      stale = true;
    };
  });
  // States are derived by the server; periodically refresh actions as time passes.
  onMount(() => {
    const timer = setInterval(() => {
      if (!busy && !confirmOpen) reload++;
    }, 60000);
    return () => clearInterval(timer);
  });
  const formId = $props.id();
  const editor = superForm(defaults(closureForm(), zod4(closureSchema)), {
    id: formId,
    SPA: true,
    validators: zod4Client(closureSchema),
    validationMethod: "onblur",
    multipleSubmits: "prevent",
    resetForm: false,
    taintedMessage: true,
    async onUpdate({ form: validated }) {
      if (!validated.valid || busy || conflict || (!selected && archived))
        return;
      if (selected && !validated.data.startsAt) {
        error = "A scheduled closure must have an explicit start.";
        return;
      }
      error = "";
      notice = "";
      busy = true;
      try {
        if (selected) {
          const paths = changedClosurePaths(baseline, validated.data);
          if (!paths.length) {
            notice = "No changes to save.";
            return;
          }
          await disablementApi.update(
            selected.id,
            selected.revision,
            closureInput(validated.data),
            paths,
          );
        } else {
          const retry = attempt.prepare(validated.data);
          retryPending = true;
          await disablementApi.create(
            locationId,
            retry.key,
            closureInput(retry.payload),
          );
        }
        attempt.clear();
        retryPending = false;
        selected = null;
        baseline = closureForm();
        editor.reset({ data: baseline, newState: baseline });
        validated.data = baseline;
        notice = "Closure saved.";
        reload++;
        onchange(locationId);
      } catch (cause) {
        if (
          cause instanceof ConnectError &&
          (cause.code === Code.Aborted ||
            cause.code === Code.FailedPrecondition)
        ) {
          if (selected) conflict = true;
          reload++;
          onchange(locationId);
        }
        error =
          cause instanceof Error && cause.message.startsWith("A creation may")
            ? cause.message
            : adminError(cause, "closure");
      } finally {
        busy = false;
      }
    },
  });
  const { form, errors, enhance, submitting, isTainted, tainted } = editor;
  function choose(item: Disablement | null) {
    if (busy || $submitting) return;
    if (isTainted($tainted) || retryPending) {
      selectAfterDiscard = item;
      confirmation = "discard";
      confirmOpen = true;
      return;
    }
    setSelected(item);
  }
  function setSelected(item: Disablement | null) {
    selected = item;
    baseline = closureForm(item ?? undefined);
    editor.reset({ data: baseline, newState: baseline });
    error = "";
    notice = "";
    conflict = false;
    attempt.clear();
    retryPending = false;
  }
  function ask(action: "end" | "cancel", item: Disablement) {
    target = item;
    confirmation = action;
    confirmOpen = true;
  }
  async function confirm() {
    if (busy) return;
    if (confirmation === "discard") {
      setSelected(selectAfterDiscard);
      confirmOpen = false;
      return;
    }
    if (confirmation === "restart") {
      attempt.clear();
      retryPending = false;
      confirmOpen = false;
      error = "";
      return;
    }
    if (!target) return;
    busy = true;
    error = "";
    notice = "";
    try {
      if (confirmation === "end") await disablementApi.end(target.id);
      else await disablementApi.cancel(target.id);
      notice =
        confirmation === "end"
          ? "Closure ended."
          : "Scheduled closure cancelled.";
      if (selected?.id === target.id) conflict = true;
      reload++;
      onchange(locationId);
    } catch (cause) {
      error = adminError(cause, "closure");
      reload++;
    } finally {
      busy = false;
      confirmOpen = false;
    }
  }
  function reconcile() {
    const latest = result?.disablements.find((d) => d.id === selected?.id);
    if (!latest || latest.state !== State.SCHEDULED) {
      error =
        "This closure is no longer scheduled on this page. Keep your draft, or cancel editing and select a scheduled closure from the refreshed history.";
      return;
    }
    const next = closureForm(latest),
      draft = $form,
      merged = { ...next };
    for (const key of ["startsAt", "endsAt", "reason"] as const)
      if (draft[key] !== baseline[key]) merged[key] = draft[key];
    selected = latest;
    baseline = next;
    editor.reset({ data: next, newState: next });
    form.set(merged, { taint: true });
    conflict = false;
    error = "";
    notice =
      "Latest closure loaded. Your edited fields were kept. Review before saving.";
  }
</script>

<Card variant="outlined"
  ><div class="panel-body">
    <header class="page-heading">
      <div>
        <h2>Temporary closures</h2>
        <p>
          Record closures without archiving the location. Locations remain
          selectable during a closure.
        </p>
      </div>
    </header>
    {#if error}<p class="notice error" role="alert">{error}</p>{/if}
    {#if notice}<p class="notice" role="status">{notice}</p>{/if}
    {#if archived && !selected}<p class="notice" role="status">
        Restore this location before creating a new closure.
      </p>{/if}
    {#if conflict}<p class="notice" role="alert">
        The closure changed. Your draft is preserved. Refresh history and
        reconcile before saving.
      </p>
      <div class="form-actions">
        <Button variant="outlined" disabled={loading} onclick={reconcile}
          >Reconcile draft with latest</Button
        >
      </div>{/if}
    <h3>{selected ? "Edit scheduled closure" : "Create closure"}</h3>
    <form class="data-form" method="POST" use:enhance>
      <fieldset
        class="form-fieldset form-grid"
        disabled={busy || $submitting || (archived && !selected)}
      >
        <div class="field">
          <TextFieldOutlined
            label={selected ? "Starts at" : "Starts at · defaults to now"}
            type="datetime-local"
            step="1"
            name="startsAt"
            bind:value={$form.startsAt}
            error={!!$errors.startsAt}
            aria-invalid={$errors.startsAt ? "true" : undefined}
            aria-describedby="closure-start-error"
          />{#if $errors.startsAt}<small
              class="field-error"
              id="closure-start-error">{$errors.startsAt[0]}</small
            >{/if}
        </div>
        <div class="field">
          <TextFieldOutlined
            label="Ends at · defaults to indefinite"
            type="datetime-local"
            step="1"
            name="endsAt"
            bind:value={$form.endsAt}
            error={!!$errors.endsAt}
            aria-invalid={$errors.endsAt ? "true" : undefined}
            aria-describedby="closure-end-error"
          />{#if $errors.endsAt}<small
              class="field-error"
              id="closure-end-error">{$errors.endsAt[0]}</small
            >{/if}
        </div>
        <div class="field wide">
          <TextFieldOutlinedMultiline
            label="Reason for closure"
            name="reason"
            required
            rows={3}
            bind:value={$form.reason}
            error={!!$errors.reason}
            aria-invalid={$errors.reason ? "true" : undefined}
            aria-describedby="closure-reason-error"
          />{#if $errors.reason}<small
              class="field-error"
              id="closure-reason-error">{$errors.reason[0]}</small
            >{/if}
        </div>
      </fieldset>
      <div class="form-actions">
        {#if selected}<Button
            type="button"
            variant="text"
            disabled={busy}
            onclick={() => choose(null)}>Cancel editing</Button
          >{/if}<Button
          type="submit"
          disabled={busy || $submitting || conflict || (archived && !selected)}
          >{busy || $submitting
            ? "Saving…"
            : selected
              ? "Save closure"
              : "Create closure"}</Button
        >
      </div>
    </form>
    {#if retryPending}<p class="notice" role="status">
        A creation has been attempted. Retry the same details safely, or check
        history before starting another.
      </p>
      <Button
        variant="text"
        onclick={() => {
          confirmation = "restart";
          confirmOpen = true;
        }}>Start a new creation attempt</Button
      >{/if}
    <div class="closure-history-controls">
      <div class="field">
        <SelectOutlined
          label="Closure state"
          options={stateOptions}
          bind:value={closureState}
          onchange={() => (page = 1)}
        />
      </div>
      <Button
        type="button"
        variant="text"
        size="xs"
        iconType="full"
        aria-label="Refresh history"
        title="Refresh history"
        disabled={loading || busy}
        onclick={() => reload++}
      >
        <svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M3 12a9 9 0 0 1 9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
          <path d="M21 12a9 9 0 0 1-9 9c-2.52 0-4.93-1-6.74-2.74L3 16" />
          <path d="M8 16H3v5" />
        </svg>
      </Button>
    </div>
    {#if loadError}<p class="notice error" role="alert">{loadError}</p>
      <Button onclick={() => reload++}>Retry closures</Button>
    {:else if loading}<p role="status">Loading closures…</p>
    {:else if result}
      <p class="secondary-text" role="status">
        {String(result.pageInfo.totalItems)} closures
      </p>
      <ul class="record-list">
        {#each result.disablements as item (item.id)}
          <li class="record-row">
            <div>
              <h3>{item.reason}</h3>
              <span class="record-state">{stateLabel(item.state)}</span>
              <p>
                {formatTimestamp(item.startsAt)} → {item.endsAt
                  ? formatTimestamp(item.endsAt)
                  : "Indefinite"}
              </p>
              {#if item.endedAt}<p>
                  Ended {formatTimestamp(item.endedAt)}
                </p>{/if}{#if item.cancelledAt}<p>
                  Cancelled {formatTimestamp(item.cancelledAt)}
                </p>{/if}
            </div>
            <div class="form-actions">
              {#if item.state === State.SCHEDULED}<Button
                  variant="text"
                  disabled={busy}
                  onclick={() => choose(item)}>Edit</Button
                ><Button
                  variant="outlined"
                  disabled={busy}
                  onclick={() => ask("cancel", item)}>Cancel closure</Button
                >{:else if item.state === State.ACTIVE}<Button
                  variant="outlined"
                  disabled={busy}
                  onclick={() => ask("end", item)}>End now</Button
                >{/if}
            </div>
          </li>
        {/each}
      </ul>
      <Pagination
        page={result.pageInfo.page}
        totalPages={result.pageInfo.totalPages}
        {loading}
        onchange={(next) => (page = next)}
      />
    {/if}
  </div></Card
>
<ConfirmDialog
  bind:open={confirmOpen}
  title={confirmation === "end"
    ? "End this closure now?"
    : confirmation === "cancel"
      ? "Cancel scheduled closure?"
      : confirmation === "discard"
        ? "Discard closure draft?"
        : "Start a new creation?"}
  label={confirmation === "end"
    ? "End closure"
    : confirmation === "cancel"
      ? "Cancel closure"
      : confirmation === "discard"
        ? "Discard draft"
        : "Start new attempt"}
  {busy}
  onconfirm={confirm}
>
  {#if confirmation === "discard"}<p>
      Your unsaved closure details will be discarded. An attempted creation may
      already have succeeded; check history before creating another closure.
    </p>
  {:else if confirmation === "restart"}<p>
      The previous creation may already have succeeded. Check history first to
      avoid a duplicate. This discards its retry key.
    </p>
  {:else}<p>{target?.reason}</p>
    <p>
      {confirmation === "end"
        ? "The closure will end immediately."
        : "The scheduled closure will not take effect."} This action is final.
    </p>{/if}
</ConfirmDialog>

<style>
  .closure-history-controls {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    align-items: center;
    gap: 0.5rem;
  }
</style>
