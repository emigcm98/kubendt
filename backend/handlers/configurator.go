package handlers

import (
	"encoding/json"
	"fmt"
	"kubendt/helpers"
	"kubendt/types"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

func ConfigureNetwork(c *gin.Context) {
	startedAt := time.Now()
	namespace := c.Param("namespace")
	var req types.ConfigureNetworkRequest

	// Strict JSON decoding: reject unknown fields so typos like 'typee' or
	// 'replicaas' are caught with a clear error instead of being silently dropped.
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		log.Println("❌ Error parsing JSON:", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// A link entry is shorthand for actions on the interfaces of its two ends.
	// It is resolved against the topology up front, so a wrong link is a 400
	// instead of a half-applied request.
	linkTargets, err := helpers.ExpandLinkShaping(namespace, req.Links)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Targets = append(req.Targets, linkTargets...)

	type ErrorEntry struct {
		Pod    string `json:"pod"`
		Driver string `json:"driver"`
		Error  string `json:"error"`
	}

	type ActionResult struct {
		Pod         string   `json:"pod"`
		ResolvedPod string   `json:"resolved_pod,omitempty"`
		Driver      string   `json:"driver"`
		Action      string   `json:"action"`
		Status      string   `json:"status"`
		Commands    []string `json:"commands,omitempty"`
		Output      string   `json:"output,omitempty"`
		Error       string   `json:"error,omitempty"`
		PodTookMs   float64  `json:"pod_took_ms,omitempty"`
	}

	type podProcessResult struct {
		index         int
		errorList     []ErrorEntry
		actionResults []ActionResult
		successes     int
		failures      int
		skipped       int
	}

	processPod := func(index int, podConfig types.PodConfig) podProcessResult {
		podStart := time.Now()
		result := podProcessResult{index: index}

		podName, err := helpers.ResolvePodReference(namespace, podConfig.Pod)
		if err != nil {
			log.Printf("❌ Could not resolve pod '%s': %v", podConfig.Pod, err)
			for _, action := range podConfig.Actions {
				result.errorList = append(result.errorList, ErrorEntry{
					Pod:    podConfig.Pod,
					Driver: "unknown",
					Error:  err.Error(),
				})
				result.actionResults = append(result.actionResults, ActionResult{
					Pod:    podConfig.Pod,
					Driver: "unknown",
					Action: action.Type,
					Status: "failed",
					Error:  err.Error(),
				})
				result.failures++
			}
			return result
		}

		// Driver lookup is non-fatal: universal actions (custom, traffic
		// control) run without one, the rest fail in the plan.
		driver, driverErr := helpers.GetDriverForPod(namespace, podName)
		driverType := ""
		if driverErr != nil {
			log.Printf("⚠️ No driver for pod '%s': %v (universal actions will still run)", podName, driverErr)
		} else {
			driverType = fmt.Sprintf("%T", driver)
		}

		// History older than the pod belongs to a previous incarnation and must
		// not make an action look already applied. Without the timestamp we fall
		// back to matching by payload alone.
		podCreated, podErr := helpers.PodCreationTime(namespace, podName)
		if podErr != nil {
			log.Printf("⚠️ Could not read creation time of pod '%s': %v (duplicate check by payload only)", podName, podErr)
		}

		plans := helpers.PlanActions(namespace, podName, driver, driverErr, podConfig.Actions)
		for i := range plans {
			p := &plans[i]
			// A repeatable action (custom) runs every time it is asked for.
			if p.Err != nil || !p.Flags.Persist || (p.Desc != nil && p.Desc.Repeatable) {
				continue
			}
			alreadyPersisted, existsErr := helpers.DriverOperationAppliedSince(namespace, podName, driverType, p.Action, podCreated)
			if existsErr != nil {
				p.Err = existsErr
			} else if alreadyPersisted {
				p.Skip = "duplicate operation: already applied to this pod with the same payload"
			}
		}

		outcomes, _ := helpers.RunPlannedActions(namespace, podName, plans, helpers.RunOptions{})

		fail := func(p helpers.PlannedAction, err error, commands []string) {
			result.errorList = append(result.errorList, ErrorEntry{Pod: podConfig.Pod, Driver: driverType, Error: err.Error()})
			result.actionResults = append(result.actionResults, ActionResult{
				Pod:         podConfig.Pod,
				ResolvedPod: podName,
				Driver:      driverType,
				Action:      p.Action.Type,
				Status:      "failed",
				Commands:    commands,
				Error:       err.Error(),
			})
			result.failures++
		}
		for i, p := range plans {
			o := outcomes[i]
			switch {
			case p.Skip != "":
				result.actionResults = append(result.actionResults, ActionResult{
					Pod:         podConfig.Pod,
					ResolvedPod: podName,
					Driver:      driverType,
					Action:      p.Action.Type,
					Status:      "skipped",
					Error:       p.Skip,
				})
				result.skipped++
			case p.Err != nil:
				log.Printf("❌ Error resolving action '%s' on '%s': %v", p.Action.Type, podName, p.Err)
				fail(p, p.Err, nil)
			case o.Err != nil:
				fail(p, o.Err, p.CommandStrings())
			default:
				if p.Flags.Persist {
					if persistErr := helpers.SaveDriverOperation(namespace, podName, driverType, p.Action); persistErr != nil {
						err := fmt.Errorf("operation executed but could not persist history: %v", persistErr)
						log.Printf("❌ %v", err)
						fail(p, err, p.CommandStrings())
						continue
					}
				}
				result.actionResults = append(result.actionResults, ActionResult{
					Pod:         podConfig.Pod,
					ResolvedPod: podName,
					Driver:      driverType,
					Action:      p.Action.Type,
					Status:      "success",
					Commands:    p.CommandStrings(),
					Output:      o.Output,
				})
				result.successes++
			}
		}

		// Stamp per-pod timing on every ActionResult of this pod.
		podTookMs := float64(time.Since(podStart).Milliseconds())
		for j := range result.actionResults {
			result.actionResults[j].PodTookMs = podTookMs
		}

		return result
	}

	poolSize := runtime.NumCPU()
	if poolSize > len(req.Targets) {
		poolSize = len(req.Targets)
	}
	if poolSize < 1 {
		poolSize = 1
	}

	jobs := make(chan podProcessResult, len(req.Targets))
	results := make(chan podProcessResult, len(req.Targets))

	var wg sync.WaitGroup
	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results <- processPod(job.index, req.Targets[job.index])
			}
		}()
	}

	for i := range req.Targets {
		jobs <- podProcessResult{index: i}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	podResultsByIndex := make([]podProcessResult, len(req.Targets))
	for result := range results {
		podResultsByIndex[result.index] = result
	}

	errorList := make([]ErrorEntry, 0)
	actionResults := make([]ActionResult, 0)
	successes := 0
	failures := 0
	skipped := 0
	for _, result := range podResultsByIndex {
		errorList = append(errorList, result.errorList...)
		actionResults = append(actionResults, result.actionResults...)
		successes += result.successes
		failures += result.failures
		skipped += result.skipped
	}

	// Sum the first ActionResult's PodTookMs per pod to get the sequential-equivalent
	// total (i.e. what the wall time would be if pods were processed one by one).
	seqMs := 0.0
	for _, r := range podResultsByIndex {
		if len(r.actionResults) > 0 {
			seqMs += r.actionResults[0].PodTookMs
		}
	}
	seqSecs := seqMs / 1000.0

	status := "success"
	if len(errorList) > 0 {
		status = "completed_with_errors"
	}
	took := time.Since(startedAt)

	speedup := 0.0
	if took.Seconds() > 0 {
		speedup = seqSecs / took.Seconds()
	}

	c.JSON(http.StatusOK, gin.H{
		"status":         status,
		"successes":      successes,
		"failures":       failures,
		"skipped":        skipped,
		"errors":         errorList,
		"action_results": actionResults,
		"took_time": gin.H{
			"total":                 fmt.Sprintf("%.2fs", took.Seconds()),
			"sequential_equivalent": fmt.Sprintf("%.2fs", seqSecs),
		},
		"speedup": speedup,
	})
}
