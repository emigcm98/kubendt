package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"kubendt/actions"
	"kubendt/capabilities"
	"kubendt/helpers"
	"kubendt/types"
)

// InspectPod runs one inspection of the pod's driver, or lists the ones it
// offers when `what` is empty. A read, never written to the history.
func InspectPod(c *gin.Context) {
	namespace := c.Param("namespace")
	podName, err := helpers.ResolvePodReference(namespace, c.Param("podName"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	driver, err := helpers.GetDriverForPod(namespace, podName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("pod %s has no driver: %v", podName, err)})
		return
	}
	what := c.Query("what")
	if what == "" {
		list := make([]types.InspectionInfo, 0)
		for _, d := range actions.InspectionsForDriver(driver) {
			list = append(list, types.InspectionInfo{ID: d.ID, Label: d.Label, Capability: d.Capability, Columns: columnsInfo(d.Columns)})
		}
		c.JSON(http.StatusOK, types.PodInspectionsResponse{Inspections: list})
		return
	}
	d, ok := actions.LookupInspection(what)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown inspection %q", what)})
		return
	}
	if !d.Supports(driver) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("the driver of %s offers no %s", podName, what)})
		return
	}
	rows, err := d.Run(driver, namespace, podName)
	note := ""
	var notRunning *capabilities.DaemonNotRunningError
	if errors.As(err, &notRunning) {
		// A router deployed before its capability existed has no daemon for
		// it: an empty read with the reason, not a failure.
		note = notRunning.Daemon + " is not running on this node"
		err = nil
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []map[string]string{}
	}
	c.JSON(http.StatusOK, types.PodInspectionResponse{What: d.ID, Label: d.Label, Capability: d.Capability, Columns: columnsInfo(d.Columns), Rows: rows, Note: note})
}

func columnsInfo(cols []actions.Column) []types.InspectionColumn {
	out := make([]types.InspectionColumn, 0, len(cols))
	for _, c := range cols {
		out = append(out, types.InspectionColumn{Name: c.Name, Label: c.Label, Description: c.Description})
	}
	return out
}
