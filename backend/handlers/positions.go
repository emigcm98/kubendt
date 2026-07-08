package handlers

import (
	"kubendt/helpers"
	"kubendt/kubeclient"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func GetNodePositions(c *gin.Context) {

	db := helpers.GetDB()
	if db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Cannot get database client"})
		return
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Cannot resolve cluster identity"})
		return
	}

	namespace := c.Param("namespace")
	query := `SELECT node_id, x, y FROM node_positions WHERE cluster_id = ? AND namespace = ?`
	rows, err := db.Query(query, clusterID, namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error de base de datos"})
		return
	}
	defer rows.Close()

	positions := make(map[string]Position)
	for rows.Next() {
		var id string
		var pos Position
		if err := rows.Scan(&id, &pos.X, &pos.Y); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading positions"})
			return
		}
		pos.X = math.Round(pos.X)
		pos.Y = math.Round(pos.Y)
		positions[id] = pos
	}

	c.JSON(http.StatusOK, positions)
}

func SaveNodePositions(c *gin.Context) {

	db := helpers.GetDB()
	if db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Cannot get database client"})
		return
	}

	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Cannot resolve cluster identity"})
		return
	}

	namespace := c.Param("namespace")
	var positions map[string]Position

	if err := c.ShouldBindJSON(&positions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error starting transaction"})
		return
	}

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO node_positions (cluster_id, namespace, node_id, x, y) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error preparando consulta"})
		return
	}
	defer stmt.Close()

	for id, pos := range positions {
		roundedX := math.Round(pos.X)
		roundedY := math.Round(pos.Y)
		_, err := stmt.Exec(clusterID, namespace, id, roundedX, roundedY)
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error saving position"})
			return
		}
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error saving positions"})
		return
	}
	c.Status(http.StatusNoContent)
}
