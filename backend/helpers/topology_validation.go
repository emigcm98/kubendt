package helpers

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	caps "kubendt/capabilities/capabilities"
	drivers_registry "kubendt/drivers/registry"
	"kubendt/types"
)

// envVarNameRe matches a valid environment variable name (C_IDENTIFIER), the
// same rule the kubelet enforces for container env keys.
var envVarNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateNodeInputs statically validates node fields before deploy: image
// presence, env var names, mount targets/existence, and device paths. Returns a
// 400-worthy error on the first problem. Image existence is not checked here
// (only knowable at pull time); see WaitForPodsReady.
func ValidateNodeInputs(namespace string, nodes []types.NodeSpec) error {
	for _, node := range nodes {
		if strings.TrimSpace(node.Image) == "" {
			return fmt.Errorf("node '%s': image is required", node.Name)
		}

		for key := range node.Env {
			if !envVarNameRe.MatchString(key) {
				return fmt.Errorf("node '%s': invalid environment variable name %q (must match [A-Za-z_][A-Za-z0-9_]*)", node.Name, key)
			}
		}

		seenTargets := make(map[string]bool, len(node.Mounts))
		for _, m := range node.Mounts {
			if strings.TrimSpace(m.File) == "" {
				return fmt.Errorf("node '%s': a mount entry is missing its 'file'", node.Name)
			}
			if err := validateContainerPath("mount target", m.MountTo); err != nil {
				return fmt.Errorf("node '%s': mount for file %q: %w", node.Name, m.File, err)
			}
			if seenTargets[m.MountTo] {
				return fmt.Errorf("node '%s': duplicate mount target %q", node.Name, m.MountTo)
			}
			seenTargets[m.MountTo] = true

			exists, err := NamespaceFileExists(namespace, m.File)
			if err != nil {
				return fmt.Errorf("node '%s': could not verify mount file %q: %w", node.Name, m.File, err)
			}
			if !exists {
				return fmt.Errorf("node '%s': mount file %q does not exist in the namespace file manager (upload it before deploying)", node.Name, m.File)
			}
		}

		for _, d := range node.Devices {
			if err := validateContainerPath("device path", d.Path); err != nil {
				return fmt.Errorf("node '%s': %w", node.Name, err)
			}
		}
	}
	return nil
}

// validateContainerPath enforces an absolute, traversal-free Linux path, the
// kind kubelet accepts as a mountPath or device path.
func validateContainerPath(kind, p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("%s is required", kind)
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%s %q must be an absolute path", kind, p)
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("%s %q contains a null byte", kind, p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("%s %q must not contain '..'", kind, p)
		}
	}
	return nil
}

// driverInstanceForNode returns a fresh driver instance for a node, resolving
// the type default when the node declares no explicit driver.
func driverInstanceForNode(n types.NodeSpec) (any, error) {
	name := strings.TrimSpace(n.Driver)
	if name == "" {
		def, err := drivers_registry.ResolveDefaultForType(n.Type)
		if err != nil {
			return nil, err
		}
		name = def
	}
	return drivers_registry.NewByName(name)
}

// ValidateLinkIPCapabilities rejects a link that assigns an IP to a node whose
// driver has no L3 support (e.g. a pure L2 switch). Link IPs are written to the
// meshnet CRD and applied by the pod, bypassing the driver, so this gate has to
// run at the deploy/modify boundary. "external" endpoints and nodes absent from
// `nodes` are skipped.
func ValidateLinkIPCapabilities(links []types.LinkSpec, nodes []types.NodeSpec) error {
	byName := make(map[string]types.NodeSpec, len(nodes))
	for _, n := range nodes {
		byName[n.Name] = n
	}

	l3Memo := make(map[string]bool)
	// supportsL3 reports (l3ok, known); known=false when the node isn't in the
	// provided set (external uplink or an endpoint we can't resolve here).
	supportsL3 := func(nodeName string) (bool, bool) {
		if nodeName == "" || nodeName == externalNodeName {
			return false, false
		}
		n, known := byName[nodeName]
		if !known {
			return false, false
		}
		if v, seen := l3Memo[nodeName]; seen {
			return v, true
		}
		v := false
		if inst, err := driverInstanceForNode(n); err == nil {
			_, v = inst.(caps.L3Capable)
		}
		l3Memo[nodeName] = v
		return v, true
	}

	reject := func(idx int, nodeName, ip string) error {
		return fmt.Errorf("link[%d]: node '%s' (type %s) has no L3 support and cannot be assigned the IP %q; use an L2 link (no IP) or a router/host",
			idx, nodeName, byName[nodeName].Type, ip)
	}

	for i, link := range links {
		if strings.TrimSpace(link.LocalIP) != "" {
			if ok, known := supportsL3(link.Node); known && !ok {
				return reject(i, link.Node, link.LocalIP)
			}
		}
		if strings.TrimSpace(link.PeerIP) != "" {
			if ok, known := supportsL3(link.PeerNode); known && !ok {
				return reject(i, link.PeerNode, link.PeerIP)
			}
		}
	}
	return nil
}

// ValidateLinkIPs rejects malformed L3 addresses before deploy. An empty IP is
// allowed (an L2-only link carries no addressing); a non-empty value must parse
// as a bare IP or a CIDR (e.g. 10.0.0.1/24).
func ValidateLinkIPs(links []types.LinkSpec) error {
	check := func(idx int, side, value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		if strings.Contains(value, "/") {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return fmt.Errorf("link[%d]: %s IP %q is not a valid CIDR (e.g. 10.0.0.1/24)", idx, side, value)
			}
			return nil
		}
		if net.ParseIP(value) == nil {
			return fmt.Errorf("link[%d]: %s IP %q is not a valid IP address", idx, side, value)
		}
		return nil
	}
	for i, link := range links {
		if err := check(i, "local", link.LocalIP); err != nil {
			return err
		}
		if err := check(i, "peer", link.PeerIP); err != nil {
			return err
		}
	}
	return nil
}
