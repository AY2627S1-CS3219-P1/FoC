package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

func deletionFact(userID uuid.UUID) models.UserDeletedOutbox {
	return models.UserDeletedOutbox{
		EventID: uuid.New(), UserID: userID, DeletionRequestID: uuid.New(),
		DeletedAt: time.Now().UTC(), PayloadVersion: 1,
	}
}

func deleteInTransaction(ctx context.Context, persistence *Store, id uuid.UUID, fact models.UserDeletedOutbox) error {
	return persistence.WithTransaction(ctx, func(tx *Store) error {
		u, err := tx.Users.GetByIDForUpdate(ctx, id)
		if err != nil {
			return err
		}
		return tx.AccountDeletion.HardDeleteAccount(ctx, u, fact)
	})
}

func TestAccountHardDeleteRetainsReviewsAndFreesEmail(t *testing.T) {
	ctx := context.Background()
	persistence, db := setupAuthStore(t)
	user := createStoreUser(t, persistence, "delete-target@example.com")
	recipient := createStoreUser(t, persistence, "retained-recipient@example.com")
	reviewID := uuid.New()
	if err := db.Exec(`INSERT INTO reviews (id, reviewer_id, reviewee_id, order_id, rating, comment)
		VALUES (?, ?, ?, ?, 4, 'retained contribution')`, reviewID, user.ID, recipient.ID, uuid.New()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE users SET review_count = 1, avg_rating = 4 WHERE id = ?", recipient.ID).Error; err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(uuid.NewString()))
	if err := db.Create(&models.Session{UserID: user.ID, TokenHash: hash[:], ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	challengeHash := sha256.Sum256([]byte(uuid.NewString()))
	// A registration challenge is email-bound with no user FK.
	if err := db.Create(&models.AuthToken{Email: user.Email, TokenHash: challengeHash[:],
		Purpose: models.TokenPurposeRegister, ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserCreatedOutbox{EventID: uuid.New(), UserID: user.ID, Email: user.Email,
		DisplayName: user.DisplayName, UserCreatedAt: user.CreatedAt, PayloadVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	fact := deletionFact(user.ID)
	if err := deleteInTransaction(ctx, persistence, user.ID, fact); err != nil {
		t.Fatal(err)
	}
	var count int64
	for table, condition := range map[string]string{
		"users": "id = ?", "sessions": "user_id = ?", "user_created_outbox": "user_id = ?",
	} {
		if err := db.Table(table).Where(condition, user.ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s not physically deleted: count=%d err=%v", table, count, err)
		}
	}
	if err := db.Unscoped().Model(&models.AuthToken{}).Where("email = ?", user.Email).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("email challenges survived: %d %v", count, err)
	}
	var retained struct {
		ReviewerID uuid.NullUUID
		RevieweeID uuid.UUID
		Comment    string
	}
	if err := db.Table("reviews").Select("reviewer_id, reviewee_id, comment").Where("id = ?", reviewID).Take(&retained).Error; err != nil {
		t.Fatal(err)
	}
	if retained.ReviewerID.Valid || retained.RevieweeID != recipient.ID || retained.Comment != "retained contribution" {
		t.Fatalf("retained review = %+v", retained)
	}
	var stats struct {
		ReviewCount int
		AvgRating   float64
	}
	if err := db.Table("users").Select("review_count, avg_rating").Where("id = ?", recipient.ID).Take(&stats).Error; err != nil || stats.ReviewCount != 1 || stats.AvgRating != 4 {
		t.Fatalf("recipient statistics changed: %+v %v", stats, err)
	}
	var publishedFact models.UserDeletedOutbox
	if err := db.Take(&publishedFact, "event_id = ?", fact.EventID).Error; err != nil || publishedFact.UserID != user.ID || publishedFact.PublishedAt != nil {
		t.Fatalf("missing pending deletion fact: %+v %v", publishedFact, err)
	}
	replacement := createStoreUser(t, persistence, user.Email)
	if replacement.ID == user.ID {
		t.Fatal("registration reused deleted identity")
	}
	if err := deleteInTransaction(ctx, persistence, user.ID, deletionFact(user.ID)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat deletion = %v", err)
	}
	if _, err := persistence.Users.GetByID(ctx, replacement.ID); err != nil {
		t.Fatalf("repeat old-UUID request affected replacement: %v", err)
	}
}

func TestAccountDeleteRollsBackWhenOutboxInsertFails(t *testing.T) {
	ctx := context.Background()
	persistence, db := setupAuthStore(t)
	u := createStoreUser(t, persistence, "rollback-deletion@example.com")
	fact := deletionFact(u.ID)
	conflict := deletionFact(uuid.New())
	conflict.EventID = fact.EventID
	if err := db.Create(&conflict).Error; err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(uuid.NewString()))
	if err := db.Create(&models.AuthToken{Email: u.Email, TokenHash: hash[:],
		Purpose: models.TokenPurposeRegister, ExpiresAt: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := deleteInTransaction(ctx, persistence, u.ID, fact); err == nil {
		t.Fatal("expected duplicate outbox event to fail")
	}
	if err := db.Unscoped().Take(&models.User{}, u.ID).Error; err != nil {
		t.Fatalf("user delete was not rolled back: %v", err)
	}
	var count int64
	if err := db.Table("auth_tokens").Where("email = ?", u.Email).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("challenge removal was not rolled back: %d %v", count, err)
	}
}

func TestAccountDeleteProtectsBootstrapAndUntrackedPrivateImage(t *testing.T) {
	persistence, db := setupAuthStore(t)
	u := createStoreUser(t, persistence, "protected-delete@example.com")
	if err := db.Create(&models.AdminBootstrap{Singleton: true, UserID: u.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := deleteInTransaction(context.Background(), persistence, u.ID, deletionFact(u.ID)); !errors.Is(err, ErrInUse) {
		t.Fatalf("bootstrap deletion = %v", err)
	}
	if err := db.Delete(&models.AdminBootstrap{}, "user_id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.User{}).Where("id = ?", u.ID).Update("profile_picture_key", "private/legacy.jpg").Error; err != nil {
		t.Fatal(err)
	}
	if err := deleteInTransaction(context.Background(), persistence, u.ID, deletionFact(u.ID)); !errors.Is(err, ErrProfileImageCleanup) {
		t.Fatalf("discarded an untracked private image: %v", err)
	}
	if err := db.Take(&models.User{}, u.ID).Error; err != nil {
		t.Fatalf("protected account missing or unreadable: %v", err)
	}
}
