<script lang="ts">
	import { TelInput } from 'svelte-tel-input';
	import type { CountryCode } from 'svelte-tel-input/types';

	// Local numbers are read as Singapore numbers, matching User Service.
	const DEFAULT_COUNTRY: CountryCode = 'SG';

	let {
		value = $bindable(''),
		valid = $bindable(true),
		label = 'Phone number',
		name = 'phoneNumber',
		id = 'phone-number',
		required = false,
		disabled = false,
		error = ''
	}: {
		/** E.164, such as +6591234567. */
		value?: string;
		valid?: boolean;
		label?: string;
		name?: string;
		id?: string;
		required?: boolean;
		disabled?: boolean;
		/** Shown under the field and marks it invalid. */
		error?: string;
	} = $props();

	let country = $state<CountryCode | null>(DEFAULT_COUNTRY);
</script>

<div class="phone-field" class:invalid={!!error} class:disabled>
	<TelInput
		{id}
		{name}
		{required}
		{disabled}
		bind:value
		bind:valid
		bind:country
		autocomplete="tel"
		class="phone-input"
		aria-invalid={error ? 'true' : undefined}
		aria-describedby={error ? `${id}-error` : undefined}
		options={{ autoPlaceholder: true, spaces: true, validateOn: 'blur' }}
	/>
	<label for={id}>{label}</label>
	<div class="outline"></div>
</div>
{#if error}<small class="field-error" id="{id}-error">{error}</small>{/if}

<style>
	.phone-field {
		position: relative;
		display: flex;
		height: 3.5rem;
		width: 100%;
	}

	.phone-field :global(.phone-input) {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		padding: 1rem;
		border: none;
		border-radius: var(--m3-shape-extra-small);
		background: transparent;
		color: var(--m3c-on-surface);
		font: inherit;
		font-size: 1rem;
		outline: none;
	}

	.phone-field :global(.phone-input::placeholder) {
		color: var(--m3c-on-surface-variant);
		opacity: 0.7;
	}

	label {
		position: absolute;
		top: 0;
		inset-inline-start: 0.75rem;
		translate: 0 -50%;
		padding: 0 0.25rem;
		font-size: 0.75rem;
		line-height: 1rem;
		color: var(--m3c-on-surface-variant);
		background-color: var(--m3v-background, var(--m3c-surface));
		pointer-events: none;
		/* Sit above the outline so the border doesn't cross the text. */
		z-index: 1;
	}

	.outline {
		position: absolute;
		inset: 0;
		border: 1px solid var(--m3c-outline);
		border-radius: var(--m3-shape-extra-small);
		pointer-events: none;
		transition: border-color 100ms;
	}

	.phone-field:hover .outline {
		border-color: var(--m3c-on-surface);
	}

	.phone-field:focus-within .outline {
		border: 2px solid var(--m3c-primary);
	}

	.phone-field:focus-within label {
		color: var(--m3c-primary);
	}

	.invalid .outline,
	.invalid:focus-within .outline {
		border-color: var(--m3c-error);
	}

	.invalid label,
	.invalid:focus-within label {
		color: var(--m3c-error);
	}

	.disabled {
		opacity: 0.38;
	}
</style>
