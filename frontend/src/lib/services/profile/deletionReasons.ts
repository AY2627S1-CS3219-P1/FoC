// Human-readable text for each waiting reason the account-deletion worker may
// report. The keys must stay in sync with the stable codes the user service
// emits (user-service/internal/accountdeletion/drain.go and worker.go); an
// unmapped code falls back to a generic line so a dependency outage still shows
// something the user can act on rather than an empty "no blockers" state.
export const deletionReasons: Record<string, string> = {
	order_outstanding: 'You still have an order that is not finished.',
	offer_open: 'You still have an open offer. Withdraw it or let the requester decide.',
	pending_workflow: 'A pending order recovery is still being resolved.',
	pending_admission: 'A pending order admission is still being processed.',
	credit_reserved_balance: 'You still hold reserved credits.',
	credit_reservation: 'Credits are still held or being settled for you.',
	credit_unsettled_assignment: 'One of your assignments is not settled yet.',
	credit_pending_financial_work: 'A pending credit settlement is still in progress.',
	admission_grace: 'Final checks wait for recently issued tokens to expire.',
	check_unavailable: 'A final check could not run yet.'
};

export function reasonText(reason: string): string {
	return deletionReasons[reason] ?? 'A final check is still waiting for: ' + reason + '.';
}
