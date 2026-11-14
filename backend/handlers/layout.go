package handlers

import (
	"net/http"

	"kubendt/helpers"
	"kubendt/types"

	"github.com/gin-gonic/gin"
)

func GetLayout(c *gin.Context) {
	l, err := helpers.GetLayout(c.Param("namespace"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, l)
}

func PutLayout(c *gin.Context) {
	var l types.Layout
	if err := c.ShouldBindJSON(&l); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}
	if err := helpers.ValidateLayout(&l); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := helpers.PutLayout(c.Param("namespace"), l); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
