package accounts

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/pquerna/otp/totp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEnable2FARequiresCurrentCodeForRotation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	account := models.User{UUID: "fixture", Username: "fixture", Passwd: "fixture"}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	existing, err := totp.Generate(totp.GenerateOpts{Issuer: "fixture", AccountName: "account"})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := totp.Generate(totp.GenerateOpts{Issuer: "fixture", AccountName: "account"})
	if err != nil {
		t.Fatal(err)
	}
	if err := enable2FA(db, account.UUID, existing.Secret(), ""); err != nil {
		t.Fatalf("first enrollment: %v", err)
	}
	code, err := totp.GenerateCode(existing.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := enable2FA(db, account.UUID, replacement.Secret(), ""); !errors.Is(err, ErrCurrent2FARequired) {
		t.Fatalf("unverified rotation: %v", err)
	}
	if err := enable2FA(db, account.UUID, replacement.Secret(), code); err != nil {
		t.Fatalf("verified rotation: %v", err)
	}
	var result models.User
	if err := db.First(&result, "uuid = ?", account.UUID).Error; err != nil {
		t.Fatal(err)
	}
	if result.TwoFactor != replacement.Secret() {
		t.Fatal("verified replacement was not persisted")
	}
}
