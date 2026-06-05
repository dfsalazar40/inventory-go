package store

// Aggregation behavior: repeated holds by the SAME user on the SAME item collapse
// into a single PENDING reservation whose quantity is the sum, rather than creating
// a new reservation row each time. Holds on different items stay separate.

import (
	"context"
	"fmt"
	"testing"
)

func TestReserve_AggregatesSameUserSameItem(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := newTestPool(t)
	ctx := context.Background()
	st := NewReservationStore(pool)

	itemID := createTestItem(t, pool, "aggregate-same", 10)

	// Reserve the same item three times as the same user (distinct idempotency keys).
	var lastID string
	for i := 0; i < 3; i++ {
		r, err := st.Reserve(ctx, ReserveParams{
			ItemID:         itemID,
			UserID:         "agg-user",
			Quantity:       1,
			IdempotencyKey: fmt.Sprintf("agg-key-%d", i),
		})
		if err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		lastID = r.ID
	}

	// Exactly one aggregated reservation, quantity summed to 3, same row each time.
	res, err := st.ListByUser(ctx, "agg-user")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 aggregated reservation, got %d", len(res))
	}
	if res[0].Quantity != 3 {
		t.Errorf("want aggregated quantity 3, got %d", res[0].Quantity)
	}
	if res[0].ID != lastID {
		t.Errorf("want stable reservation id %s, got %s", lastID, res[0].ID)
	}
	if got := itemReserved(t, pool, itemID); got != 3 {
		t.Errorf("want items.reserved=3, got %d", got)
	}
}

func TestReserve_DifferentItemsStaySeparate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := newTestPool(t)
	ctx := context.Background()
	st := NewReservationStore(pool)

	itemA := createTestItem(t, pool, "agg-item-a", 10)
	itemB := createTestItem(t, pool, "agg-item-b", 10)

	if _, err := st.Reserve(ctx, ReserveParams{
		ItemID: itemA, UserID: "agg-user-2", Quantity: 1, IdempotencyKey: "agg2-a",
	}); err != nil {
		t.Fatalf("reserve A: %v", err)
	}
	if _, err := st.Reserve(ctx, ReserveParams{
		ItemID: itemB, UserID: "agg-user-2", Quantity: 1, IdempotencyKey: "agg2-b",
	}); err != nil {
		t.Fatalf("reserve B: %v", err)
	}

	res, err := st.ListByUser(ctx, "agg-user-2")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 separate reservations for different items, got %d", len(res))
	}
}
