package helpers

import "testing"

func TestImageRef(t *testing.T) {
	for ref, want := range map[string][2]string{
		"alpine":                                      {"docker.io/library/alpine", ":latest"},
		"alpine:3.24.2":                               {"docker.io/library/alpine", ":3.24.2"},
		"docker.io/library/alpine:3.24.2":             {"docker.io/library/alpine", ":3.24.2"},
		"docker.io/alpine:3.24.2":                     {"docker.io/library/alpine", ":3.24.2"},
		"globocom/openvswitch:latest":                 {"docker.io/globocom/openvswitch", ":latest"},
		"globocom/openvswitch@sha256:abc":             {"docker.io/globocom/openvswitch", "@sha256:abc"},
		"quay.io/frrouting/frr:10.7.1":                {"quay.io/frrouting/frr", ":10.7.1"},
		"localhost/vyos-router:dev":                   {"localhost/vyos-router", ":dev"},
		"localhost:5000/kubendt/toolbox:1.8.0":        {"localhost:5000/kubendt/toolbox", ":1.8.0"},
		"ghcr.io/herlesupreeth/docker_open5gs:master": {"ghcr.io/herlesupreeth/docker_open5gs", ":master"},
	} {
		name, version := imageRef(ref)
		if name != want[0] || version != want[1] {
			t.Errorf("%s: %s %s, want %s %s", ref, name, version, want[0], want[1])
		}
	}
}

func TestImageKnown(t *testing.T) {
	known := []string{"quay.io/frrouting/frr:10.7.1", "globocom/openvswitch@sha256:abc", "alpine:3.24.2"}
	for image, want := range map[string]bool{
		"quay.io/frrouting/frr:10.7.1":              true,
		"quay.io/frrouting/frr:10.6.0":              false,
		"quay.io/frrouting/frr":                     false,
		"docker.io/library/alpine:3.24.2":           true,
		"alpine":                                    false,
		"globocom/openvswitch@sha256:abc":           true,
		"docker.io/globocom/openvswitch@sha256:abc": true,
		"globocom/openvswitch:latest":               false,
	} {
		if got := ImageKnown(known, image); got != want {
			t.Errorf("%s: %v, want %v", image, got, want)
		}
	}
}
