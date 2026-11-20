package helpers

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"kubendt/kubeclient"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MigrateAllNamespaces moves at start what a database or the disk kept for
// every lab namespace into the cluster: the layout, the link attributes,
// the history and the files. Each store does it on first touch anyway; the
// pass is for a backend carried into the cluster, or whose volume is about
// to go, so that nothing waits for a touch that may never come. Nothing to
// do without a database or a files directory for this cluster.
func MigrateAllNamespaces() error {
	hasDisk := false
	if clusterID, err := kubeclient.CurrentClusterID(); err == nil {
		if _, err := os.Stat(filepath.Join(filesBasePath(), clusterID)); err == nil {
			hasDisk = true
		}
	}
	if DB == nil && !hasDisk {
		return nil
	}
	list, err := stateNamespaces().List(context.TODO(), metav1.ListOptions{LabelSelector: "kubendt/enabled=true"})
	if err != nil {
		return fmt.Errorf("could not list the namespaces: %w", err)
	}
	steps := []struct {
		what string
		run  func(string) error
	}{
		{"layout", func(ns string) error { _, err := GetLayout(ns); return err }},
		{"link attributes", migrateLinkAttributes},
		{"history", migrateDriverHistory},
		{"files", migrateNamespaceFiles},
	}
	checked := 0
	for _, ns := range list.Items {
		if ns.DeletionTimestamp != nil {
			continue
		}
		checked++
		for _, step := range steps {
			if err := step.run(ns.Name); err != nil {
				log.Printf("⚠️ Could not move the %s of namespace '%s' into the cluster: %v", step.what, ns.Name, err)
			}
		}
	}
	log.Printf("ℹ️ Looked for state kept outside the cluster in %d namespace(s); what there was has moved", checked)
	return nil
}
