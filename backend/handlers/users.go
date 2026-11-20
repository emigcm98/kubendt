package handlers

import (
	"errors"
	"net/http"
	"strings"

	"kubendt/auth"
	"kubendt/helpers"

	"github.com/gin-gonic/gin"
)

// The accounts, managed by admins and the owner over a session or the
// password, never over an API token. What each may do to whom is the auth
// package's rule; here it is only told.

func userError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, auth.ErrUserExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrBadRole):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, auth.ErrNotAllowed):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "FORBIDDEN"})
	case errors.Is(err, auth.ErrNoStore):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Load a kubeconfig first, the users are kept in the cluster"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// GET /users
func ListUsers(c *gin.Context) {
	users, err := auth.ListUsers()
	if err != nil {
		userError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// POST /users  -> a new account; without a password one is generated and
// returned once. The account changes it at its first login either way.
func CreateUser(c *gin.Context) {
	var req struct {
		Username            string `json:"username"`
		Password            string `json:"password"`
		Role                string `json:"role"`
		CanCreateNamespaces bool   `json:"can_create_namespaces"`
		QuotaNamespaces     int    `json:"quota_namespaces"`
		QuotaPods           int    `json:"quota_pods"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Username) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A username is required"})
		return
	}
	generated, err := auth.CreateUser(PrincipalOf(c), req.Username, req.Password, req.Role, req.CanCreateNamespaces, req.QuotaNamespaces, req.QuotaPods)
	if err != nil {
		userError(c, err)
		return
	}
	u, _ := auth.GetUser(strings.TrimSpace(req.Username))
	out := gin.H{"user": u}
	if generated != "" {
		out["password"] = generated
	}
	c.JSON(http.StatusCreated, out)
}

// PUT /users/:name  -> role, standing, flags or quotas
func UpdateUser(c *gin.Context) {
	var patch auth.UserPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
		return
	}
	if err := auth.UpdateUser(PrincipalOf(c), c.Param("name"), patch); err != nil {
		userError(c, err)
		return
	}
	u, _ := auth.GetUser(c.Param("name"))
	c.JSON(http.StatusOK, gin.H{"user": u})
}

// DELETE /users/:name?reassign_to=<user>  -> the account goes, with its
// tokens; the namespaces it owned pass to reassign_to, or to the admins
// alone when nobody is named, and it leaves every guest list.
func DeleteUser(c *gin.Context) {
	name := c.Param("name")
	newOwner := strings.TrimSpace(c.Query("reassign_to"))
	if newOwner != "" {
		if _, ok := auth.GetUser(newOwner); !ok || newOwner == name {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no such user to reassign to: " + newOwner})
			return
		}
	}
	if err := auth.DeleteUser(PrincipalOf(c), name); err != nil {
		userError(c, err)
		return
	}
	if owned, guested, err := helpers.RemoveAccountFromNamespaces(name, newOwner); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "user deleted, but its namespaces could not all be updated", "error": err.Error()})
		return
	} else {
		c.JSON(http.StatusOK, gin.H{"message": "user deleted", "namespaces_owned": owned, "namespaces_guest_of": guested, "reassigned_to": newOwner})
	}
}

// POST /users/:name/password  -> a reset: the given password or a generated
// one, returned once, to change at the next login
func ResetUserPassword(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&req)
	generated, err := auth.ResetPassword(PrincipalOf(c), c.Param("name"), req.Password)
	if err != nil {
		userError(c, err)
		return
	}
	out := gin.H{"message": "password reset"}
	if generated != "" {
		out["password"] = generated
	}
	c.JSON(http.StatusOK, out)
}
