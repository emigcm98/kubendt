package handlers

import (
	"errors"
	"net/http"

	"kubendt/auth"
	"kubendt/settings"

	"github.com/gin-gonic/gin"
)

// The owner may change some of the environment's settings from the portal;
// any admin may read them.
func mayEditSettings(c *gin.Context) bool {
	if !auth.Enabled() {
		return true
	}
	p := PrincipalOf(c)
	return p != nil && p.Role == auth.RoleOwner
}

// GET /settings -> every setting the portal may change, with its value and
// where it comes from: saved, the environment or the default.
func ListSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"settings": settings.List(), "editable": mayEditSettings(c)})
}

// PUT /settings -> {"values": {"KUBENDT_LINK_MTU": "1400", "KUBENDT_PLACEMENT": null}}:
// a value is saved, null goes back to the environment. Every value is checked
// before any is saved.
func SaveSettings(c *gin.Context) {
	if !mayEditSettings(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only the owner changes the settings.", "code": "OWNER_ONLY"})
		return
	}
	var req struct {
		Values map[string]*string `json:"values"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Values) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "values, a map of settings to change, is required"})
		return
	}
	if err := settings.Save(req.Values); err != nil {
		var bad *settings.Error
		if errors.As(err, &bad) {
			c.JSON(http.StatusBadRequest, gin.H{"error": bad.Error(), "code": "INVALID_SETTING", "key": bad.Key})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings.List(), "editable": true})
}
