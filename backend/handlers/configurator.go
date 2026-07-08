package handlers

import (
	"encoding/json"
	"fmt"
	"kubendt/executor"
	"kubendt/helpers"
	"kubendt/types"
	"log"
	"net/http"
	"runtime"
	"strings"
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

		// Driver lookup is non-fatal: custom actions don't need a driver.
		// driverErr is checked per-action in the plan phase below.
		driver, driverErr := helpers.GetDriverForPod(namespace, podName)

		var driverType string
		var driverExecutor executor.CommandExecutor
		var driverExecutorName string

		if driverErr != nil {
			log.Printf("⚠️ No driver for pod '%s': %v (custom actions will still run)", podName, driverErr)
		} else {
			driverType = fmt.Sprintf("%T", driver)
			var execErr error
			driverExecutor, driverExecutorName, execErr = executor.ResolveForDriver(driver)
			if execErr != nil {
				log.Printf("❌ Could not resolve executor for driver '%s' on pod '%s': %v", driverType, podName, execErr)
				for _, action := range podConfig.Actions {
					result.errorList = append(result.errorList, ErrorEntry{
						Pod:    podConfig.Pod,
						Driver: driverType,
						Error:  execErr.Error(),
					})
					result.actionResults = append(result.actionResults, ActionResult{
						Pod:         podConfig.Pod,
						ResolvedPod: podName,
						Driver:      driverType,
						Action:      action.Type,
						Status:      "failed",
						Error:       execErr.Error(),
					})
					result.failures++
				}
				return result
			}
			log.Printf("ℹ️ Driver '%s' on pod '%s' will use executor '%s'", driverType, podName, driverExecutorName)
		}

		// ── PLAN PHASE ──────────────────────────────────────────────────────
		// Resolve every action upfront so we can batch consecutive same-executor
		// actions in the EXECUTE PHASE.
		type plannedAction struct {
			action     types.ActionEntry
			flags      types.ActionFlags
			execInst   executor.CommandExecutor
			execName   string
			commands   [][]string
			cmdStrings []string // pre-computed for reporting
			skipReason string   // non-empty → skip (not an error)
			err        error    // non-nil → fail during planning
		}

		plans := make([]plannedAction, 0, len(podConfig.Actions))
		for _, action := range podConfig.Actions {
			p := plannedAction{action: action, flags: helpers.ResolveActionFlags(action)}

			// custom actions bypass driver/capability entirely, always use kubectl.
			if action.Type == "custom" {
				args, normErr := helpers.NormalizeCustomCommand(action.Command)
				if normErr != nil {
					p.err = normErr
					plans = append(plans, p)
					continue
				}
				kubectlExec, execGetErr := executor.Get(executor.DefaultExecutorName)
				if execGetErr != nil {
					p.err = execGetErr
					plans = append(plans, p)
					continue
				}
				p.commands = [][]string{args}
				p.execName = executor.DefaultExecutorName
				p.execInst = kubectlExec
				structured := executor.CommandsFromLegacyForExecutor(p.commands, executor.DefaultExecutorName)
				strs := make([]string, 0, len(structured))
				for _, cmd := range structured {
					strs = append(strs, cmd.String())
				}
				p.cmdStrings = strs
				plans = append(plans, p)
				continue
			}

			// Non-custom actions require a driver. Fail if none was found.
			if driverErr != nil {
				p.err = fmt.Errorf("no driver for pod '%s': %v", podName, driverErr)
				plans = append(plans, p)
				continue
			}

			if p.flags.Persist {
				alreadyPersisted, existsErr := helpers.DriverOperationExists(namespace, podName, driverType, action)
				if existsErr != nil {
					p.err = existsErr
					plans = append(plans, p)
					continue
				}
				if alreadyPersisted {
					p.skipReason = "duplicate operation: already persisted with same payload"
					plans = append(plans, p)
					continue
				}
			}

			actionExecutorName, commands, resolveErr := helpers.ResolveDriverExecutionPlanForPod(namespace, podName, driver, action)
			if resolveErr != nil {
				p.err = resolveErr
				plans = append(plans, p)
				continue
			}
			if commands == nil {
				p.err = fmt.Errorf("unrecognized or unsupported action: %s", action.Type)
				plans = append(plans, p)
				continue
			}

			p.execName = actionExecutorName
			p.commands = commands

			execInst := driverExecutor
			if strings.TrimSpace(actionExecutorName) != "" && actionExecutorName != driverExecutorName {
				override, execErr := executor.Get(actionExecutorName)
				if execErr != nil {
					p.err = execErr
					plans = append(plans, p)
					continue
				}
				execInst = override
			}
			p.execInst = execInst

			structured := executor.CommandsFromLegacyForExecutor(commands, actionExecutorName)
			strs := make([]string, 0, len(structured))
			for _, cmd := range structured {
				strs = append(strs, cmd.String())
			}
			p.cmdStrings = strs

			plans = append(plans, p)
		}

		// ── EXECUTE PHASE ────────────────────────────────────────────────────
		// Merge consecutive same-batchable-executor actions (vyos_apply /
		// xr_apply) into one Command to avoid N configure→commit round trips.
		i := 0
		for i < len(plans) {
			p := plans[i]

			// ── Skip ───────────────────────────────────────────────────────
			if p.skipReason != "" {
				result.actionResults = append(result.actionResults, ActionResult{
					Pod:         podConfig.Pod,
					ResolvedPod: podName,
					Driver:      driverType,
					Action:      p.action.Type,
					Status:      "skipped",
					Error:       p.skipReason,
				})
				result.skipped++
				i++
				continue
			}

			// ── Planning error ────────────────────────────────────────────
			if p.err != nil {
				log.Printf("❌ Error resolving action '%s' on '%s': %v", p.action.Type, podName, p.err)
				result.errorList = append(result.errorList, ErrorEntry{
					Pod:    podConfig.Pod,
					Driver: driverType,
					Error:  p.err.Error(),
				})
				result.actionResults = append(result.actionResults, ActionResult{
					Pod:         podConfig.Pod,
					ResolvedPod: podName,
					Driver:      driverType,
					Action:      p.action.Type,
					Status:      "failed",
					Error:       p.err.Error(),
				})
				result.failures++
				i++
				continue
			}

			// ── Batchable group ───────────────────────────────────────────
			// Collect the longest consecutive run of ready (non-skip, non-error)
			// actions sharing the same batchable executor with no output capture.
			if executor.BatchableExecutors[p.execName] && !p.flags.Get {
				j := i
				group := make([]plannedAction, 0)
				for j < len(plans) {
					gp := plans[j]
					if gp.skipReason != "" || gp.err != nil || gp.execName != p.execName || gp.flags.Get {
						break
					}
					group = append(group, gp)
					j++
				}

				// Flatten all action command groups into one Args list.
				var allArgs []string
				for _, gp := range group {
					for _, cmdGroup := range gp.commands {
						allArgs = append(allArgs, cmdGroup...)
					}
				}

				batchCmd := executor.NewArgsCommand(allArgs)
				batchOut, execErr := p.execInst.ExecCommandAndGet(podName, namespace, batchCmd)
				if batchOut != "" {
					log.Printf("ℹ️ Batch exec output [%s] pod=%s:\n%s", p.execName, podName, batchOut)
				}

				for _, gp := range group {
					if execErr != nil {
						log.Printf("❌ Batch exec error on '%s' pod '%s': %v", gp.action.Type, podName, execErr)
						result.errorList = append(result.errorList, ErrorEntry{
							Pod:    podConfig.Pod,
							Driver: driverType,
							Error:  execErr.Error(),
						})
						result.actionResults = append(result.actionResults, ActionResult{
							Pod:         podConfig.Pod,
							ResolvedPod: podName,
							Driver:      driverType,
							Action:      gp.action.Type,
							Status:      "failed",
							Commands:    gp.cmdStrings,
							Error:       execErr.Error(),
						})
						result.failures++
					} else {
						errText := ""
						if gp.flags.Persist {
							if persistErr := helpers.SaveDriverOperation(namespace, podName, driverType, gp.action); persistErr != nil {
								errText = fmt.Sprintf("operation executed but could not persist history: %v", persistErr)
								result.errorList = append(result.errorList, ErrorEntry{
									Pod:    podConfig.Pod,
									Driver: driverType,
									Error:  errText,
								})
								result.failures++
								log.Printf("❌ %s", errText)
							}
						}
						actionStatus := "success"
						if errText != "" {
							actionStatus = "failed"
						} else {
							result.successes++
						}
						result.actionResults = append(result.actionResults, ActionResult{
							Pod:         podConfig.Pod,
							ResolvedPod: podName,
							Driver:      driverType,
							Action:      gp.action.Type,
							Status:      actionStatus,
							Commands:    gp.cmdStrings,
							Error:       errText,
						})
					}
				}

				i = j
				continue
			}

			// ── Normal (non-batchable) execution ──────────────────────────
			structuredCommands := executor.CommandsFromLegacyForExecutor(p.commands, p.execName)
			commandOutputs := make([]string, 0, len(structuredCommands))
			actionErrors := make([]string, 0)
			for _, cmd := range structuredCommands {
				var execErr error
				if p.flags.Get {
					var output string
					output, execErr = p.execInst.ExecCommandAndGet(podName, namespace, cmd)
					if execErr == nil && strings.TrimSpace(output) != "" {
						commandOutputs = append(commandOutputs, strings.TrimSpace(output))
					}
				} else {
					execErr = p.execInst.ExecCommand(podName, namespace, cmd)
				}
				if execErr != nil {
					log.Printf("❌ Error executing command on '%s': %v", podName, execErr)
					result.errorList = append(result.errorList, ErrorEntry{
						Pod:    podConfig.Pod,
						Driver: driverType,
						Error:  execErr.Error(),
					})
					actionErrors = append(actionErrors, execErr.Error())
					result.failures++
				} else {
					result.successes++
				}
			}

			actionStatus := "success"
			actionErrorText := ""
			if len(actionErrors) > 0 {
				actionStatus = "failed"
				actionErrorText = strings.Join(actionErrors, " | ")
			} else if p.flags.Persist {
				if persistErr := helpers.SaveDriverOperation(namespace, podName, driverType, p.action); persistErr != nil {
					actionStatus = "failed"
					actionErrorText = fmt.Sprintf("operation executed but could not persist history: %v", persistErr)
					result.errorList = append(result.errorList, ErrorEntry{
						Pod:    podConfig.Pod,
						Driver: driverType,
						Error:  actionErrorText,
					})
					result.failures++
					log.Printf("❌ %s", actionErrorText)
				}
			}

			result.actionResults = append(result.actionResults, ActionResult{
				Pod:         podConfig.Pod,
				ResolvedPod: podName,
				Driver:      driverType,
				Action:      p.action.Type,
				Status:      actionStatus,
				Commands:    p.cmdStrings,
				Output:      strings.Join(commandOutputs, "\n"),
				Error:       actionErrorText,
			})

			i++
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
