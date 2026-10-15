package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

func TestSQLFixturesPreserveUsersResetAndRejectIdentityConflicts(t *testing.T) {
	_, db := setupAuthStore(t)
	ctx := context.Background()
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "seeds", "users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	runSQL := func(reset bool) error {
		conn, err := sqlDB.Conn(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		value := "'false'"
		if reset {
			value = "'true'"
		}
		_, err = conn.ExecContext(ctx, strings.ReplaceAll(string(script), ":'reset_demo'", value))
		if err != nil {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
		return err
	}
	run := func(reset bool) {
		t.Helper()
		if err := runSQL(reset); err != nil {
			t.Fatal(err)
		}
	}
	list := func() []models.User {
		t.Helper()
		var users []models.User
		if err := db.Unscoped().Order("id").Find(&users).Error; err != nil {
			t.Fatal(err)
		}
		return users
	}
	run(false)
	before := list()
	if len(before) != 6 {
		t.Fatalf("got %d users", len(before))
	}
	run(false)
	if !reflect.DeepEqual(before, list()) {
		t.Fatal("normal seed changed users")
	}
	var admin, other models.User
	if err := db.Where("email = ?", "admin@demo.foc.test").Take(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("email = ?", "other@demo.foc.test").Take(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", admin.ID).Updates(map[string]any{"role": "user", "description": "Edited"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&other).Error; err != nil {
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
	if err := db.Take(&got, other.ID).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&models.RoleChange{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("audit count %d: %v", count, err)
	}
	if err := db.Model(&models.AllowedEmailDomain{}).Where("domain = ?", "demo.foc.test").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("domain count %d: %v", count, err)
	}
	got = models.User{}
	if err := db.Take(&got, unrelated.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", other.ID).Update("email", "changed@example.com").Error; err != nil {
		t.Fatal(err)
	}
	before = list()
	if err := runSQL(true); err == nil {
		t.Fatal("expected identity conflict")
	}
	if !reflect.DeepEqual(before, list()) {
		t.Fatal("identity conflict did not roll back")
	}
}
