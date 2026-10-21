package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
)

const userID = "22222222-2222-4222-8222-222222222222"

type fakeStore struct {
	missing       bool
	reads         []string
	total         int64
	limit, offset int64
	listed        bool
	opened        []string
}

func (f *fakeStore) Open(_ context.Context, id string, _ time.Time) error {
	f.opened = append(f.opened, id)
	return nil
}

func (f *fakeStore) Account(_ context.Context, id string) (account.Account, error) {
	f.reads = append(f.reads, id)
	if f.missing {
		return account.Account{}, account.ErrAccountNotFound
	}
	return account.Account{UserID: id, Balance: 100, Reserved: 30}, nil
}

func (f *fakeStore) LedgerEntries(_ context.Context, _ string, limit, offset int64) ([]account.LedgerEntry, int64, error) {
	f.limit, f.offset, f.listed = limit, offset, true
	return []account.LedgerEntry{{Kind: account.Grant, BalanceDelta: 100}}, f.total, nil
}

func TestGetAccountReportsAvailableCredits(t *testing.T) {
	store := &fakeStore{}
	a, err := account.NewService(store, store, time.Now).GetAccount(context.Background(), userID)
	if err != nil || a.Available() != 70 {
		t.Fatalf("account: %+v %v", a, err)
	}
	if _, err := account.NewService(store, store, time.Now).GetAccount(context.Background(), "nope"); !errors.Is(err, account.ErrInvalidArgument) {
		t.Fatalf("invalid id: %v", err)
	}
}

func TestReadsReportAMissingAccount(t *testing.T) {
	store := &fakeStore{missing: true}
	s := account.NewService(store, store, time.Now)

	if _, err := s.GetAccount(context.Background(), userID); !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("get: %v", err)
	}
	if _, err := s.ListLedgerEntries(context.Background(), userID, account.Page{}); !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("list: %v", err)
	}
	if store.listed {
		t.Fatal("listed entries for a missing account")
	}
}

func TestListLedgerEntriesPages(t *testing.T) {
	tests := []struct {
		name          string
		page          account.Page
		total         int64
		limit, offset int64
		info          account.PageInfo
	}{
		{"defaults", account.Page{}, 0, 20, 0, account.PageInfo{Page: 1, PageSize: 20}},
		{"third page", account.Page{Number: 3, Size: 10}, 25, 10, 20, account.PageInfo{Page: 3, PageSize: 10, TotalItems: 25, TotalPages: 3}},
		{"maximum size", account.Page{Size: 100}, 100, 100, 0, account.PageInfo{Page: 1, PageSize: 100, TotalItems: 100, TotalPages: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{total: tt.total}
			got, err := account.NewService(store, store, time.Now).ListLedgerEntries(context.Background(), userID, tt.page)
			if err != nil {
				t.Fatal(err)
			}
			if got.PageInfo != tt.info || store.limit != tt.limit || store.offset != tt.offset || len(store.reads) != 1 {
				t.Fatalf("page %+v limit %d offset %d reads %v", got.PageInfo, store.limit, store.offset, store.reads)
			}
		})
	}
	for _, p := range []account.Page{{Number: -1}, {Size: -1}, {Size: 101}} {
		if _, err := account.NewService(&fakeStore{}, nil, time.Now).ListLedgerEntries(context.Background(), userID, p); !errors.Is(err, account.ErrInvalidArgument) {
			t.Fatalf("page %+v: %v", p, err)
		}
	}
}

func TestOpenValidatesTheUserID(t *testing.T) {
	store := &fakeStore{}
	s := account.NewService(store, store, time.Now)

	if err := s.Open(context.Background(), "22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatal(err)
	}
	if err := s.Open(context.Background(), "nope"); !errors.Is(err, account.ErrInvalidArgument) {
		t.Fatalf("invalid id: %v", err)
	}
	if len(store.opened) != 1 || store.opened[0] != userID {
		t.Fatalf("opened %v", store.opened)
	}
}
