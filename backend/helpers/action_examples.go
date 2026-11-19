package helpers

import (
	_ "embed"
	"encoding/json"
	"sync"

	drivers_meta "kubendt/drivers/meta"
	"kubendt/types"
)

// One sample per action, the parameters a reader would expect: the plan
// test pins what every driver sends for them, and the drivers listing shows
// the commands they resolve to.
//
//go:embed action_samples.json
var actionSamplesJSON []byte

var (
	samplesOnce sync.Once
	samples     map[string]types.ActionEntry
)

// ActionSamples is the sample of every action, by type.
func ActionSamples() map[string]types.ActionEntry {
	samplesOnce.Do(func() {
		var entries []types.ActionEntry
		if err := json.Unmarshal(actionSamplesJSON, &entries); err != nil {
			panic("helpers: action_samples.json: " + err.Error())
		}
		samples = make(map[string]types.ActionEntry, len(entries))
		for _, e := range entries {
			samples[e.Type] = e
		}
	})
	return samples
}

// ActionExample is what an action resolves to for its sample parameters on
// one driver: the parameters and the commands as the executor runs them.
type ActionExample struct {
	Params   map[string]any
	Commands []string
}

// PlansOffline says whether the driver's commands resolve without a pod.
func PlansOffline(driver any) bool {
	pb, ok := driver.(drivers_meta.PodBoundPlanner)
	return !ok || !pb.PlansFromPod()
}

var (
	examplesMu    sync.Mutex
	examplesCache = map[string]map[string]ActionExample{}
)

// ActionExamples resolves every sample on the driver, by action type, and
// keeps the result, since drivers are stateless and the samples fixed. An
// action the driver lacks, or one whose plan needs more than the sample (a
// file to read), has no example. Nil for a driver that plans from the pod.
func ActionExamples(name string, driver any) map[string]ActionExample {
	if driver == nil || !PlansOffline(driver) {
		return nil
	}
	examplesMu.Lock()
	defer examplesMu.Unlock()
	if ex, ok := examplesCache[name]; ok {
		return ex
	}
	ex := map[string]ActionExample{}
	for actionType, sample := range ActionSamples() {
		execName, commands, err := ResolveDriverExecutionPlanForPod("", "", driver, sample)
		if err != nil || commands == nil {
			continue
		}
		ex[actionType] = ActionExample{
			Params:   sampleParams(sample),
			Commands: PlannedAction{Executor: execName, Commands: commands}.CommandStrings(),
		}
	}
	examplesCache[name] = ex
	return ex
}

// sampleParams is the sample as a request carries it, without the type.
func sampleParams(sample types.ActionEntry) map[string]any {
	raw, err := json.Marshal(sample)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	delete(out, "type")
	return out
}
