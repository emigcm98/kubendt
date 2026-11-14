package handlers

import (
	"net/http"

	"kubendt/helpers"
	"kubendt/types"

	"github.com/gin-gonic/gin"
)

// The positions endpoints are the layout document's positions, kept for the
// clients that only know them.

func GetNodePositions(c *gin.Context) {
	l, err := helpers.GetLayout(c.Param("namespace"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, l.Positions)
}

func SaveNodePositions(c *gin.Context) {
	var positions map[string]types.NodePosition
	if err := c.ShouldBindJSON(&positions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}
	if err := helpers.SavePositions(c.Param("namespace"), positions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
