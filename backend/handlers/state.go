package handlers

import (
	"net/http"

	"kubendt/helpers"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// GET /state  -> every lab namespace and the system one, with their objects
func GetClusterState(c *gin.Context) {
	namespaces, err := helpers.ListStateNamespaces()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"namespaces": namespaces})
}

// GET /state/:namespace  -> the objects of one namespace, a lab or the system one
func GetNamespaceState(c *gin.Context) {
	namespace := c.Param("namespace")
	objects, err := helpers.ListStateObjects(namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	role := "lab"
	if namespace == helpers.SystemNamespace() {
		role = "system"
	}
	c.JSON(http.StatusOK, gin.H{"namespace": namespace, "role": role, "objects": objects})
}

// GET /state/:namespace/:kind/:name  -> one object, as a document and as YAML
func GetStateObject(c *gin.Context) {
	namespace, kind, name := c.Param("namespace"), c.Param("kind"), c.Param("name")
	doc, text, err := helpers.GetStateObject(namespace, kind, name)
	if err != nil {
		switch {
		case apierrors.IsNotFound(err):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case doc == nil && text == "" && isUnknownKind(err):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"namespace": namespace, "kind": kind, "name": name, "object": doc, "yaml": text})
}

func isUnknownKind(err error) bool {
	return len(err.Error()) > 12 && err.Error()[:12] == "unknown kind"
}
