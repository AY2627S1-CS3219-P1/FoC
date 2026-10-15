package order

import "context"

// Mutate is the shared local mutation boundary. build authorizes the actor,
// validates operation-specific prerequisites and constructs a change from the
// locked aggregate and fresh relationships. A nil change is a recognized repeat.
// Do all remote work before entering this function; saga result application must
// verify the persisted operation identity in build after reacquiring this lock.
func Mutate(ctx context.Context, repo Repository, id string, build func(Order, []Offer) (*Change, error)) error {
	return repo.Within(ctx, func(tx Tx) error {
		current, err := tx.LockOrder(ctx, id)
		if err != nil {
			return err
		}
		offers, err := tx.Relationships(ctx, id)
		if err != nil {
			return err
		}
		change, err := build(current, offers)
		if err != nil {
			return err
		}
		if change == nil {
			return nil
		}
		if change.Order.ID != id || change.ExpectedRevision != current.Revision {
			return ErrConflict
		}
		return tx.Apply(ctx, *change)
	})
}
