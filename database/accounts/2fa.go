package accounts

import (
	"errors"
	"image"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var (
	TwoFactorIssuer = "Komari Monitor"
)

func Generate2Fa() (string, image.Image, error) {
	otp, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TwoFactorIssuer,
		AccountName: "komari",
	})
	if err != nil {
		return "", nil, err
	}
	img, err := otp.Image(250, 250)
	if err != nil {
		return "", nil, err
	}
	return otp.Secret(), img, nil
}

var ErrCurrent2FARequired = errors.New("a valid current 2FA code is required")

func Enable2Fa(uuid, secret, currentCode string) error {
	return enable2FA(dbcore.GetDBInstance(), uuid, secret, currentCode)
}

func enable2FA(db *gorm.DB, uuid, secret, currentCode string) error {
	db = db.Session(&gorm.Session{Logger: db.Logger.LogMode(gormlogger.Silent)})
	var user models.User
	if err := db.Where("uuid = ?", uuid).First(&user).Error; err != nil {
		return err
	}
	if user.TwoFactor != "" && !totp.Validate(currentCode, user.TwoFactor) {
		return ErrCurrent2FARequired
	}
	// A concurrent enrollment or rotation cannot be overwritten using stale verification.
	result := db.Model(&models.User{}).
		Where("uuid = ? AND COALESCE(two_factor, '') = ?", uuid, user.TwoFactor).
		Update("two_factor", secret)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCurrent2FARequired
	}
	return nil
}

func Verify2Fa(uuid, code string) (bool, error) {
	db := dbcore.GetDBInstance()
	var user models.User
	err := db.Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return false, err
	}

	if user.TwoFactor == "" {
		return false, nil // 用户未启用2FA
	}

	valid := totp.Validate(code, user.TwoFactor)
	if !valid {
		return false, nil
	}

	return true, nil
}

func Disable2Fa(uuid string) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.User{}).Where("uuid = ?", uuid).Update("two_factor", "").Error
}
