package auth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProfileMoodAndStagedPasswordResetPersistence(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	userID, sessionID := uuid.NewString(), uuid.NewString()
	username := "profile-reset-" + strings.ReplaceAll(userID, "-", "")
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,status,openim_user_id) VALUES($1,$2,'old','active',$3)`, userID, username, "im-"+userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })
	email := "person+" + strings.ReplaceAll(userID, "-", "") + "@example.test"
	phone := "+1" + fmt.Sprintf("%010d", time.Now().UnixNano()%10_000_000_000)
	if _, err = pool.Exec(ctx, `INSERT INTO user_contacts(user_id,kind,value_normalized,verified_at,is_primary) VALUES($1,'email',$2,now(),true),($1,'phone',$3,now(),true)`, userID, email, phone); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO device_sessions(id,user_id,refresh_token_hash,platform_id,expires_at) VALUES($1,$2,$3,'ios',now()+interval '1 hour')`, sessionID, userID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO openim_device_tokens(session_id,token_hash,token_ciphertext,platform_id,expires_at) VALUES($1,$2,'ciphertext',1,now()+interval '1 hour')`, sessionID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	repo := NewUserRepository(pool)
	mood := "Available 🌿"
	if err = repo.SetMood(ctx, userID, &mood); err != nil {
		t.Fatal(err)
	}
	profile, err := repo.Profile(ctx, userID)
	if err != nil || profile.Mood == nil || *profile.Mood != mood || profile.Email == nil || *profile.Email != email || !profile.EmailVerified || profile.Phone == nil || *profile.Phone != phone || !profile.PhoneVerified {
		t.Fatalf("profile=%+v err=%v", profile, err)
	}
	pingID, err := repo.PingIDForUser(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	options, err := repo.RecoveryOptionsForPingID(ctx, pingID)
	if err != nil || options.Email == nil || *options.Email != email || options.Phone == nil || *options.Phone != phone {
		t.Fatalf("recovery options=%+v err=%v", options, err)
	}
	if _, err = repo.AddContact(ctx, userID, "email", "replacement@example.test"); err != ErrContactAlreadyVerified {
		t.Fatalf("verified email mutation error=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET is_admin=true WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if isAdmin, err := repo.IsAdmin(ctx, userID); err != nil || !isAdmin {
		t.Fatalf("admin=%v err=%v", isAdmin, err)
	}
	adminEmails, err := repo.AdminEmails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundAdminEmail := false
	for _, candidate := range adminEmails {
		if candidate == email {
			foundAdminEmail = true
		}
	}
	if !foundAdminEmail {
		t.Fatalf("verified administrator email %q missing from %v", email, adminEmails)
	}
	if err = repo.SetMood(ctx, userID, nil); err != nil {
		t.Fatal(err)
	}
	profile, err = repo.Profile(ctx, userID)
	if err != nil || profile.Mood != nil {
		t.Fatalf("cleared profile mood=%v err=%v", profile.Mood, err)
	}

	challengeID, err := repo.CreatePasswordChallenge(ctx, userID, "recovery", "challenge-hash", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	resetHash := "reset-hash-" + userID
	if err = repo.ConsumeRecoveryChallengeAndCreateResetToken(ctx, challengeID, resetHash, time.Now().Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	openIMUserID, platforms, err := repo.CompletePasswordReset(ctx, resetHash, "new-password-hash")
	if err != nil || openIMUserID != "im-"+userID || len(platforms) != 1 || platforms[0] != "ios" {
		t.Fatalf("reset result openIM=%q platforms=%v err=%v", openIMUserID, platforms, err)
	}
	var sessionRevoked, tokenRevoked bool
	if err = pool.QueryRow(ctx, `SELECT s.revoked_at IS NOT NULL,t.revoked_at IS NOT NULL FROM device_sessions s JOIN openim_device_tokens t ON t.session_id=s.id WHERE s.id=$1`, sessionID).Scan(&sessionRevoked, &tokenRevoked); err != nil {
		t.Fatal(err)
	}
	if !sessionRevoked || !tokenRevoked {
		t.Fatalf("session revoked=%v OpenIM token revoked=%v", sessionRevoked, tokenRevoked)
	}
	if _, _, err = repo.CompletePasswordReset(ctx, resetHash, "second-password"); err == nil {
		t.Fatal("reset token was reusable")
	}
}
