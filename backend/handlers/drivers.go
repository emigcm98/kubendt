// pkg/handlers/drivers.go
package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	helpers "kubendt/helpers"
	"kubendt/types"

	"github.com/gin-gonic/gin"
)

type ParamInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type MethodInfo struct {
	Name   string      `json:"name"`
	Label  string      `json:"label"`
	Params []ParamInfo `json:"params"`
}

type CapabilityInfo struct {
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	Description string       `json:"description"`
	Methods     []MethodInfo `json:"methods"`
}

// InterfaceNameConstraintsInfo is the wire DTO for a driver's pod-side
// interface naming rules. Mirrors drivers_meta.InterfaceNameConstraints, but
// serialises the regexp as its source string for the JSON consumer.
type InterfaceNameConstraintsInfo struct {
	Pattern      string   `json:"pattern"`
	PatternHuman string   `json:"patternHuman"`
	Reserved     []string `json:"reserved"`
}

type DriverWithCapabilities struct {
	Name                     string                        `json:"name"`
	Type                     string                        `json:"type"`
	Executor                 string                        `json:"executor"`
	IsDefault                bool                          `json:"isDefault"`
	Capabilities             []CapabilityInfo              `json:"capabilities"`
	InterfaceNameConstraints *InterfaceNameConstraintsInfo `json:"interfaceNameConstraints,omitempty"`
}

type DriverOperationHistoryEntry struct {
	ID         int64             `json:"id"`
	PodName    string            `json:"pod_name"`
	DriverType string            `json:"driver_type"`
	ActionType string            `json:"action_type"`
	ExecutedAt string            `json:"executed_at"`
	Action     types.ActionEntry `json:"action"`
	Commands   []string          `json:"commands,omitempty"`
}

func resolveDriverHistoryCommands(namespace, podName string, action types.ActionEntry) []string {
	driver, err := helpers.GetDriverForPod(namespace, podName)
	if err != nil {
		return nil
	}

	_, commands, err := helpers.ResolveDriverExecutionPlanForPod(namespace, podName, driver, action)
	if err != nil {
		return nil
	}
	if len(commands) == 0 {
		return nil
	}

	output := make([]string, 0, len(commands))
	for _, cmd := range commands {
		output = append(output, strings.Join(cmd, " "))
	}
	return output
}

func buildDriverOperationHistoryEntries(namespace string, ops []helpers.PersistedDriverOperation) []DriverOperationHistoryEntry {
	entries := make([]DriverOperationHistoryEntry, 0, len(ops))
	for _, op := range ops {
		actionType := op.ActionType
		if actionType == "" {
			actionType = op.Action.Type
		}

		entries = append(entries, DriverOperationHistoryEntry{
			ID:         op.ID,
			PodName:    op.PodName,
			DriverType: op.DriverType,
			ActionType: actionType,
			ExecutedAt: op.ExecutedAt,
			Action:     op.Action,
			Commands:   resolveDriverHistoryCommands(namespace, op.PodName, op.Action),
		})
	}
	return entries
}

// driverCapsToResponse converts the helper-level DriverCaps into the wire DTO.
// Lives next to GetDrivers / GetDriver so both list and item endpoints stay
// byte-identical on the per-driver shape.
func driverCapsToResponse(d helpers.DriverCaps) DriverWithCapabilities {
	caps := make([]CapabilityInfo, 0, len(d.Capabilities))
	for _, cd := range d.Capabilities {
		methodsParams := helpers.ExtractMethodParametersMap(cd.Methods())
		methods := make([]MethodInfo, 0, len(cd.Methods()))
		for methodName, label := range cd.Methods() {
			params := make([]ParamInfo, 0)
			if methParams, ok := methodsParams[methodName]; ok {
				for _, p := range methParams {
					params = append(params, ParamInfo{Name: p.Name, Type: p.Type})
				}
			}
			methods = append(methods, MethodInfo{
				Name:   methodName,
				Label:  label,
				Params: params,
			})
		}
		caps = append(caps, CapabilityInfo{
			ID:          cd.ID(),
			Label:       cd.Label(),
			Description: cd.Description(),
			Methods:     methods,
		})
	}

	var constraints *InterfaceNameConstraintsInfo
	if d.InterfaceNameConstraints != nil {
		c := d.InterfaceNameConstraints
		pattern := ""
		if c.Pattern != nil {
			pattern = c.Pattern.String()
		}
		reserved := append([]string(nil), c.Reserved...)
		constraints = &InterfaceNameConstraintsInfo{
			Pattern:      pattern,
			PatternHuman: c.PatternHuman,
			Reserved:     reserved,
		}
	}

	return DriverWithCapabilities{
		Name:                     d.Name,
		Type:                     d.Type,
		Executor:                 d.Executor,
		IsDefault:                d.IsDefault,
		Capabilities:             caps,
		InterfaceNameConstraints: constraints,
	}
}

// GET /drivers/  -> ahora incluye capabilities para cada driver
func GetDrivers(c *gin.Context) {
	all, err := helpers.ListAllDriversCaps()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := make([]DriverWithCapabilities, 0, len(all))
	for _, d := range all {
		resp = append(resp, driverCapsToResponse(d))
	}

	c.JSON(http.StatusOK, gin.H{"drivers": resp})
}

// GET /drivers/:driver  -> full driver info (same per-item shape as GET /drivers/).
func GetDriver(c *gin.Context) {
	name := c.Param("driver")

	dc, err := helpers.ResolveDriverCaps(name)
	if err != nil {
		if err.Error() == `driver "`+name+`" not registered` {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, driverCapsToResponse(dc))
}

// GET /drivers/history/:namespace/:podName  -> persisted operations for one pod
func GetPodDriverOperationHistory(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")

	podName, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ops, err := helpers.ListDriverOperationsForPod(namespace, podName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	entries := buildDriverOperationHistoryEntries(namespace, ops)

	c.JSON(http.StatusOK, gin.H{
		"namespace":  namespace,
		"pod":        podName,
		"operations": entries,
	})
}

// GET /drivers/history/:namespace  -> persisted operations for all pods in namespace
func GetNamespaceDriverOperationHistory(c *gin.Context) {
	namespace := c.Param("namespace")

	ops, err := helpers.ListDriverOperationsForNamespace(namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"namespace":  namespace,
		"operations": buildDriverOperationHistoryEntries(namespace, ops),
	})
}

// DELETE /drivers/history/:id  -> delete one persisted operation by operation ID
func DeleteDriverOperationHistory(c *gin.Context) {
	idParam := c.Param("id")
	id, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid operation id"})
		return
	}

	err = helpers.DeleteDriverOperationHistoryByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "operation not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "operation deleted", "id": id})
}

// DELETE /drivers/history/namespace/:namespace/pod/:podName  -> delete persisted operations for one pod
func DeletePodDriverOperationHistory(c *gin.Context) {
	namespace := c.Param("namespace")
	podRef := c.Param("podName")

	podName, err := helpers.ResolvePodReference(namespace, podRef)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := helpers.DeleteDriverOperationHistoryForPod(namespace, podName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "pod history deleted",
		"namespace": namespace,
		"pod":       podName,
	})
}

// DELETE /drivers/history/namespace/:namespace  -> delete persisted operations for all pods in namespace
func DeleteNamespaceDriverOperationHistory(c *gin.Context) {
	namespace := c.Param("namespace")

	if err := helpers.DeleteNamespaceDriverOperationHistory(namespace); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "namespace history deleted",
		"namespace": namespace,
	})
}
