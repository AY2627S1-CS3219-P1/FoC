package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/seed"
)

func TestDemoSeedPreservesEditsResetsAndRejectsIdentityConflicts(t *testing.T) {
	_, db := setupAuthStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	data := seed.DemoDataset(now)
	adapter := NewDemoSeed(db)
	run := func(reset bool) {
		t.Helper()
		if err := adapter.SeedDemo(ctx, seed.DemoDataset(now.Add(time.Hour)), reset); err != nil {
			t.Fatal(err)
		}
	}
	if err := adapter.SeedDemo(ctx, data, false); err != nil {
		t.Fatal(err)
	}
	list := func() []models.User {
		t.Helper()
		var users []models.User
		if err := db.Unscoped().Order("id").Find(&users).Error; err != nil {
			t.Fatal(err)
		}
		return users
	}
	before := list()
	if len(before) != 6 {
		t.Fatalf("got %d users", len(before))
	}
	run(false)
	if !reflect.DeepEqual(before, list()) {
		t.Fatal("normal seed changed existing users")
	}
	admin := data.Users[1]
	if err := db.Model(&models.User{}).Where("id = ?", admin.ID).Updates(map[string]any{"role": "user", "description": "Edited"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&data.Users[4]).Error; err != nil {
		t.Fatal(err)
	}
	before = list()
	run(false)
	if !reflect.DeepEqual(before, list()) {
		t.Fatal("normal seed overwrote edits/deletion")
	}
	unrelated := models.User{Email: "unrelated@example.com", DisplayName: "Unrelated", Role: models.RoleUser}
	if err := db.Create(&unrelated).Error; err != nil {
		t.Fatal(err)
	}
	run(true)
	var got models.User
	if err := db.Take(&got, admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Role != models.RoleAdmin || got.Description != admin.Description {
		t.Fatalf("reset admin: %+v", got)
	}
	got = models.User{}
	if err := db.Take(&got, data.Users[4].ID).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&models.RoleChange{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("audit count %d: %v", count, err)
	}
	if err := db.Model(&models.AllowedEmailDomain{}).Where("domain = ?", data.Domain.Domain).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("domain count %d: %v", count, err)
	}
	got = models.User{}
	if err := db.Take(&got, unrelated.ID).Error; err != nil {
		t.Fatal(err)
	}
	// A conflict late in the dataset must also roll back earlier account edits.
	if err := db.Model(&models.User{}).Where("id = ?", data.Users[5].ID).Update("email", "changed@example.com").Error; err != nil {
		t.Fatal(err)
	}
	before = list()
	if err := adapter.SeedDemo(ctx, data, true); err == nil {
		t.Fatal("expected identity conflict")
	}
	if !reflect.DeepEqual(before, list()) {
		t.Fatal("identity conflict did not roll back")
	}
}
