package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"kubendt/actions"
	drivers_meta "kubendt/drivers/meta"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/executor"
	"kubendt/kubeclient"
	"kubendt/types"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NormalizeCustomCommand converts the raw JSON command field into a []string
// ready for kubectl exec. A JSON string is wrapped in ["sh", "-c", <cmd>] so
// that pipes, redirects and shell builtins work. A JSON array is used as-is.
func NormalizeCustomCommand(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("custom action requires a non-empty 'command' field")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("custom action 'command' string must not be empty")
		}
		return []string{"sh", "-c", s}, nil
	}
	var args []string
	if err := json.Unmarshal(raw, &args); err == nil {
		if len(args) == 0 {
			return nil, fmt.Errorf("custom action 'command' array must not be empty")
		}
		return args, nil
	}
	return nil, fmt.Errorf("custom action 'command' must be a string or an array of strings")
}

// custom bypasses the drivers: the command runs as given on any pod, through
// kubectl unless the request names another executor (a VyOS guest shell, for
// one). It runs every time it is asked for, and never counts as a duplicate.
func init() {
	actions.Register[any](
		actions.Capability{ID: "custom", Label: "Custom command", Description: "A command run as given, outside the driver."},
		actions.Action[any]{Type: "custom", Label: "Run a command", Params: actions.P(actions.Command, actions.Executor),
			Executor: executor.DefaultExecutorName, ExecutorFromRequest: true, Repeatable: true,
			Run: func(_ any, a types.ActionEntry) ([][]string, error) {
				args, err := NormalizeCustomCommand(a.Command)
				if err != nil {
					return nil, err
				}
				return [][]string{args}, nil
			}},
	)
}

// ResolveActionFlags returns ActionFlags for one action.
// Defaults are persist=true and get=false, and per-action options override them.
func ResolveActionFlags(action types.ActionEntry) types.ActionFlags {
	flags := types.DefaultActionFlags()
	if action.Options == nil {
		return flags
	}
	if action.Options.PersistHistory != nil {
		flags.Persist = *action.Options.PersistHistory
	}
	if action.Options.CaptureOutput != nil {
		flags.Get = *action.Options.CaptureOutput
	}
	return flags
}

func GetDriverForPod(namespace, podName string) (interface{}, error) {
	pod, err := kubeclient.Clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return GetDriverForListedPod(pod)
}

func GetDriverForListedPod(pod *v1.Pod) (interface{}, error) {
	if pod == nil {
		return nil, fmt.Errorf("pod is nil")
	}

	drvName := pod.Labels["kubendt/driver"]
	if drvName == "" {
		return nil, fmt.Errorf("pod %s/%s has no label kubendt/driver", pod.Namespace, pod.Name)
	}

	drv, err := drivers_registry.NewByName(drvName)
	if err != nil {
		return nil, fmt.Errorf("driver %q not registered for pod %s/%s: %w", drvName, pod.Namespace, pod.Name, err)
	}

	return drv, nil
}

// ResolveDriverExecutionPlanForPod resolves the executor and the commands of
// one action on one pod. The action is looked up in the registry, validated
// and checked against the driver's capabilities before anything is built, so
// a bad request fails here with the field named and nothing reaches the pod.
// A driver that names interfaces differently in its guest maps them before
// the capability method runs. The executor is the action's own (universal
// actions), the one the request names (custom), or the driver's default.
func ResolveDriverExecutionPlanForPod(namespace, podName string, driver interface{}, action types.ActionEntry) (string, [][]string, error) {
	desc, ok := actions.Lookup(action.Type)
	if !ok {
		return "", nil, fmt.Errorf("unknown action %q", action.Type)
	}
	if err := desc.Validate(action); err != nil {
		return "", nil, err
	}
	if !desc.Supports(driver) {
		name, typ := driverNameType(driver)
		return "", nil, fmt.Errorf("node '%s' (type %s, driver %s) does not support %s operations, so action %q cannot be applied",
			podName, typ, name, strings.TrimSuffix(desc.Capability, "Capable"), action.Type)
	}

	if mapper, ok := driver.(drivers_meta.GuestInterfaceMapper); ok {
		mapped, err := desc.MapInterfaces(action, func(name string) (string, error) {
			return mapper.GuestInterface(namespace, podName, name)
		})
		if err != nil {
			return "", nil, err
		}
		action = mapped
	}

	commands, err := desc.Run(driver, action)
	if err != nil {
		return "", nil, err
	}

	executorName := desc.Executor
	if desc.ExecutorFromRequest && action.Executor != "" {
		executorName = action.Executor
	}
	if executorName == "" {
		if _, executorName, err = executor.ResolveForDriver(driver); err != nil {
			return "", nil, err
		}
	}
	return executorName, commands, nil
}

// driverNameType best-effort extracts a driver's registered name and logical
// type for error messages.
func driverNameType(driver interface{}) (name, typ string) {
	if d, ok := driver.(interface {
		Name() string
		Type() string
	}); ok {
		return d.Name(), d.Type()
	}
	return "unknown", "unknown"
}
