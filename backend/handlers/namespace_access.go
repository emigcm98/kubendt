package handlers

import (
	"net/http"
	"strings"

	"kubendt/auth"
	"kubendt/helpers"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Who may do what in a namespace, read by anyone who sees it and written by
// its owner or an admin: the owner, and the guests with their level.

// GET /namespaces/access/:namespace
func GetNamespaceAccess(c *gin.Context) {
	namespace := c.Param("namespace")
	access, err := helpers.ReadNamespaceAccess(namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "namespace not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"namespace": namespace, "owner": access.Owner, "guests": access.Guests})
}

// PUT /namespaces/access/:namespace  -> the owner and the guests, whole
func PutNamespaceAccess(c *gin.Context) {
	namespace := c.Param("namespace")
	var req struct {
		Owner  string            `json:"owner"`
		Guests map[string]string `json:"guests"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
		return
	}
	req.Owner = strings.TrimSpace(req.Owner)
	if req.Guests == nil {
		req.Guests = map[string]string{}
	}
	if req.Owner == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A namespace has an owner"})
		return
	}
	// Every account named must exist, and the owner is nobody's guest.
	if auth.Enabled() {
		if _, ok := auth.GetUser(req.Owner); !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no such user: " + req.Owner})
			return
		}
		for name := range req.Guests {
			if _, ok := auth.GetUser(name); !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "no such user: " + name})
				return
			}
		}
	}
	delete(req.Guests, req.Owner)
	for name, level := range req.Guests {
		if !auth.ValidLevel(level) {
			c.JSON(http.StatusBadRequest, gin.H{"error": name + ": a guest's level is viewer, operator or editor"})
			return
		}
	}
	if _, err := helpers.ReadNamespaceAccess(namespace); err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "namespace not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := helpers.SetNamespaceOwner(namespace, req.Owner); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := helpers.SetNamespaceGuests(namespace, req.Guests); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"namespace": namespace, "owner": req.Owner, "guests": req.Guests})
}
