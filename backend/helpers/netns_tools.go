package helpers

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"kubendt/executor"
)

// The netns tools (ip, bridge, tc, iptables) run where the pod has them: in
// its main container when the image ships them, in the toolbox when not. The
// toolbox shares the pod's network namespace, so the outcome is the same;
// what differs is one more container on the pod. The choice is per pod and
// per tool class and stays while the pod lives; a recreated pod runs the same
// image, so it is kept across restarts too, the toolbox re-injected on demand.

type toolClass string

const (
	toolNone     toolClass = ""         // files of the pod, vtysh, custom: the pod itself
	toolNetns    toolClass = "netns"    // ip, bridge, tc
	toolIptables toolClass = "iptables" // iptables, ip6tables
)

var toolInToolbox sync.Map // "namespace/pod/class" -> bool

// A tool counts when it is the command, or a command of a shell script: at
// the start or right after a shell operator. "no ip route" inside a vtysh
// argument is not one.
var (
	netnsToolRe    = regexp.MustCompile(`(?:^|[;&|({!]\s*)(ip|bridge|tc)(?:\s|$)`)
	iptablesToolRe = regexp.MustCompile(`(?:^|[;&|({!]\s*)(iptables|ip6tables)(?:\s|$)`)
)

// toolClassOf says which netns tools a command needs: from argv[0] for a
// plain command, from the script for `sh -c`.
func toolClassOf(argv []string) toolClass {
	if len(argv) == 0 {
		return toolNone
	}
	text := argv[0]
	if (argv[0] == "sh" || argv[0] == "bash") && len(argv) >= 3 && argv[1] == "-c" {
		text = argv[2]
	}
	switch {
	case iptablesToolRe.MatchString(text):
		return toolIptables
	case netnsToolRe.MatchString(text):
		return toolNetns
	}
	return toolNone
}

// toolClassOfRows is the class of an action: iptables wins when any row needs
// it, the toolbox has both.
func toolClassOfRows(rows [][]string) toolClass {
	class := toolNone
	for _, r := range rows {
		switch toolClassOf(r) {
		case toolIptables:
			return toolIptables
		case toolNetns:
			class = toolNetns
		}
	}
	return class
}

func toolProbe(class toolClass) string {
	if class == toolIptables {
		return "iptables"
	}
	return "ip"
}

// NetnsContainer is the container that runs commands of the class on the pod:
// "" for the pod's own, the toolbox's name otherwise, injected when the pod
// has none yet. The first call for a class asks the pod once.
func NetnsContainer(namespace, podName string, class toolClass) (string, error) {
	if class == toolNone {
		return "", nil
	}
	key := namespace + "/" + podName + "/" + string(class)
	if v, ok := toolInToolbox.Load(key); ok {
		if !v.(bool) {
			return "", nil
		}
		return toolboxFor(namespace, podName)
	}
	if _, _, err := ExecInPod(namespace, podName, []string{"sh", "-c", "command -v " + toolProbe(class)}); err == nil {
		toolInToolbox.Store(key, false)
		return "", nil
	}
	toolInToolbox.Store(key, true)
	return toolboxFor(namespace, podName)
}

func toolboxFor(namespace, podName string) (string, error) {
	c, _, err := EnsureToolbox(namespace, podName, nil)
	if err != nil {
		return "", fmt.Errorf("could not ensure the toolbox: %w", err)
	}
	return c, nil
}

// ExecNetns runs a command that needs a netns tool where the pod has it. On
// first contact it tries the pod and moves to the toolbox only when the tool
// is missing, so an image that ships it never pays an extra exec.
func ExecNetns(namespace, podName string, argv []string) (stdout, stderr string, err error) {
	class := toolClassOf(argv)
	if class == toolNone {
		return ExecInPod(namespace, podName, argv)
	}
	key := namespace + "/" + podName + "/" + string(class)
	if v, ok := toolInToolbox.Load(key); ok && v.(bool) {
		return execNetnsInToolbox(namespace, podName, argv)
	}
	stdout, stderr, err = ExecInPod(namespace, podName, argv)
	switch {
	case err == nil:
		toolInToolbox.Store(key, false)
	case isBinaryNotFound(err, stderr) || executor.MissingProgram(stderr) != "":
		toolInToolbox.Store(key, true)
		return execNetnsInToolbox(namespace, podName, argv)
	}
	return stdout, stderr, err
}

func execNetnsInToolbox(namespace, podName string, argv []string) (string, string, error) {
	c, err := toolboxFor(namespace, podName)
	if err != nil {
		return "", "", err
	}
	stdout, stderr, err := ExecInContainer(namespace, podName, c, argv)
	return strings.TrimSpace(stdout), strings.TrimSpace(stderr), err
}

func init() {
	executor.NetnsReadHook = func(namespace, pod string, command []string) ([]byte, error) {
		out, errOut, err := ExecNetns(namespace, pod, command)
		if err != nil {
			return nil, fmt.Errorf("exec error: %v\n%s", err, errOut)
		}
		return []byte(out), nil
	}
}
