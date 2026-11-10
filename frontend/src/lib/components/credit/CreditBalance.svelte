<script lang="ts">
 import { creditService } from '$lib/services';
</script>

{#if creditService.account}
 <span class="credit-balance" aria-live="polite" title={creditService.failed ? 'Balance may be out of date' : undefined}>{creditService.account.available.toString()} credits</span>
{/if}
