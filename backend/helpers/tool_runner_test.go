package helpers

import (
	"strings"
	"testing"

	drivers_meta "kubendt/drivers/meta"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type containerDriver struct{}

type bareVMDriver struct{}

func (bareVMDriver) Runtime() string { return drivers_meta.RuntimeQEMU }

type vmDriver struct{ bareVMDriver }

func (vmDriver) GuestWrapper() []string { return []string{"ssh_guest", "sudo"} }
func (vmDriver) GuestTools() []string   { return []string{drivers_meta.ToolTrace} }

func fakePod(driver string) *v1.Pod {
	return &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "r1-0", Labels: map[string]string{"kubendt/driver": driver}}}
}

func TestDriverTools(t *testing.T) {
	if got := DriverTools(containerDriver{}); strings.Join(got, ",") != "trace,iperf" {
		t.Errorf("a container driver offers every tool, got %v", got)
	}
	if got := DriverTools(bareVMDriver{}); len(got) != 0 {
		t.Errorf("a VM driver without the interface offers nothing, got %v", got)
	}
	if got := DriverTools(vmDriver{}); strings.Join(got, ",") != "trace" {
		t.Errorf("a VM driver offers what it declares, got %v", got)
	}
}

func TestGuestToolRun(t *testing.T) {
	wrapper, err := guestToolRun(fakePod("VMDriver"), vmDriver{}, drivers_meta.ToolTrace)
	if err != nil || strings.Join(wrapper, " ") != "ssh_guest sudo" {
		t.Fatalf("declared tool: %v %v", wrapper, err)
	}
	if _, err := guestToolRun(fakePod("VMDriver"), vmDriver{}, drivers_meta.ToolIperf); err == nil ||
		!strings.Contains(err.Error(), "r1-0 has no iperf") || !strings.Contains(err.Error(), "VMDriver") {
		t.Errorf("an undeclared tool names the node and the driver, got %v", err)
	}
	if _, err := guestToolRun(fakePod("BareVM"), bareVMDriver{}, drivers_meta.ToolTrace); err == nil {
		t.Errorf("a VM driver without the interface refuses every tool")
	}
}

func TestToolRunWrap(t *testing.T) {
	plain := ToolRun{Container: "toolbox-1"}
	if got := plain.Wrap([]string{"mtr", "-n"}); strings.Join(got, " ") != "mtr -n" {
		t.Errorf("toolbox run leaves the argv alone, got %v", got)
	}
	guest := ToolRun{Container: "vyos", Wrapper: []string{"ssh_qemu", "sudo"}}
	if got := guest.Wrap([]string{"mtr", "-n"}); strings.Join(got, " ") != "ssh_qemu sudo mtr -n" {
		t.Errorf("guest run prefixes the argv, got %v", got)
	}
}
