package admin

import (
	"errors"
	"image/png"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/web/api"
	"github.com/pquerna/otp/totp"
)

func Generate2FA(c *gin.Context) {
	if !require2FAUser(c) {
		return
	}
	c.Header("Cache-Control", "no-store")
	secret, img, err := accounts.Generate2Fa()
	if err != nil {
		api.RespondError(c, 500, "Failed to generate 2FA: "+err.Error())
		return
	}
	c.SetCookie("2fa_secret", secret, 1800, "/", "", false, true)
	c.Header("Content-Type", "image/png")
	c.Writer.WriteHeader(200)
	png.Encode(c.Writer, img)
}

func Enable2FA(c *gin.Context) {
	if !require2FAUser(c) {
		return
	}
	currentCode := c.GetHeader("X-2FA-Code")
	if currentCode == "" {
		currentCode = c.GetHeader("X-Two-Factor-Code")
	}
	secret, _ := c.Cookie("2fa_secret")
	code := c.Query("code")
	if secret == "" || code == "" {
		api.RespondError(c, 400, "2FA secret or code not provided")
		return
	}
	if !totp.Validate(code, secret) {
		api.RespondError(c, 400, "Invalid 2FA code")
		return
	}
	err := accounts.Enable2Fa(api.GetPrincipal(c).UserUUID, secret, currentCode)
	if errors.Is(err, accounts.ErrCurrent2FARequired) {
		api.RespondError(c, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to enable 2FA")
		return
	}
	c.SetCookie("2fa_secret", "", -1, "/", "", false, true)

	api.RespondSuccess(c, "2FA enabled successfully")
}

func require2FAUser(c *gin.Context) bool {
	p := api.GetPrincipal(c)
	if p != nil && p.Type == rpc.PrincipalUser && p.UserUUID != "" {
		return true
	}
	api.RespondError(c, http.StatusUnauthorized, "A user session is required")
	return false
}

func Disable2FA(c *gin.Context) {
	if !require2FAUser(c) {
		return
	}
	err := accounts.Disable2Fa(api.GetPrincipal(c).UserUUID)
	if err != nil {
		api.RespondError(c, 500, "Failed to disable 2FA: "+err.Error())
		return
	}
	api.RespondSuccess(c, "")
}
