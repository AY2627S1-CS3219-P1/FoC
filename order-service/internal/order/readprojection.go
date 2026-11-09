package order

// ProjectForCaller returns a response-only copy. Historical participation and
// admin role alone do not authorize exact delivery information. Case-admin
// authorization is fail-closed until an authoritative open-case grant is wired.
func ProjectForCaller(c Caller, original Order) Order {
	view := cloneOrder(original)
	if original.HasParticipant(c.ID) {
		return view
	}

	view.Delivery = LocationSnapshot{BuildingName: original.Delivery.BuildingName}
	view.DeliveryInstructions = nil
	// Disposition and closure text can contain an exact destination.
	view.ItemLocation = nil
	view.CloseReason = nil
	return view
}
