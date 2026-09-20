package handlers

import (
	"log"
	"net/http"
	"strings"

	"kubendt/helpers"

	"github.com/gin-gonic/gin"
)

// GetLinks lists the namespace's links as they exist on the cluster, with
// the worker behind each endpoint and how Meshnet realized the link.
func GetLinks(c *gin.Context) {
	namespace := c.Param("namespace")

	if err := helpers.ValidateNamespaceEnabled(namespace); err != nil {
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "is not enabled for KubeNDT"):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	links, err := helpers.BuildLinkStatus(namespace)
	if err != nil {
		log.Printf("❌ Could not build link status for %s: %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"links": links})
}
