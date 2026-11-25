package helpers

import "sync"

// ForgetClusterCaches drops what this process remembers by namespace or pod,
// since after a switch to another cluster the same names are other objects:
// whether a pod's tools sit in its toolbox, the toolbox locks, and which
// namespaces already had their old files and history moved in.
func ForgetClusterCaches() {
	for _, m := range []*sync.Map{&toolInToolbox, &tcUseToolbox, &toolboxLocks, &migratedNamespaces, &migratedHistory} {
		m.Range(func(k, _ any) bool {
			m.Delete(k)
			return true
		})
	}
}
