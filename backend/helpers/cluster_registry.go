package helpers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"kubendt/kubeclient"
)

// clusterManifestEntry is one cluster's human-readable metadata, keyed by its
// canonical ID in clusters.json.
type clusterManifestEntry struct {
	Context  string `json:"context"`
	Server   string `json:"server"`
	LastSeen string `json:"lastSeen"`
}

// RecordActiveCluster upserts the active cluster into the clusters table and
// regenerates <FILES_BASE_PATH>/clusters.json, mapping the opaque cluster ID to
// a context name and server so the on-disk per-cluster folders are readable.
// Best-effort; callers ignore the error so it never blocks a context switch.
func RecordActiveCluster() error {
	if DB == nil {
		return nil
	}

	id, contextName, server, err := kubeclient.ActiveClusterMeta()
	if err != nil {
		// No reachable cluster yet: nothing to record.
		return nil
	}

	if _, err := DB.Exec(`
		INSERT INTO clusters (cluster_id, context_name, server, last_seen)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(cluster_id) DO UPDATE SET
			context_name = excluded.context_name,
			server = excluded.server,
			last_seen = CURRENT_TIMESTAMP
	`, id, contextName, server); err != nil {
		return fmt.Errorf("error recording active cluster %s: %w", id, err)
	}

	return writeClustersManifest()
}

func writeClustersManifest() error {
	rows, err := DB.Query(`SELECT cluster_id, context_name, server, last_seen FROM clusters ORDER BY cluster_id`)
	if err != nil {
		return fmt.Errorf("error reading clusters registry: %w", err)
	}
	defer rows.Close()

	manifest := make(map[string]clusterManifestEntry)
	for rows.Next() {
		var id, context, server, lastSeen string
		if err := rows.Scan(&id, &context, &server, &lastSeen); err != nil {
			return fmt.Errorf("error scanning clusters registry row: %w", err)
		}
		manifest[id] = clusterManifestEntry{Context: context, Server: server, LastSeen: lastSeen}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating clusters registry: %w", err)
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling clusters manifest: %w", err)
	}

	base := filesBasePath()
	if err := os.MkdirAll(base, 0755); err != nil {
		return fmt.Errorf("error creating files base path for clusters manifest: %w", err)
	}
	manifestPath := filepath.Join(base, "clusters.json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("error writing clusters manifest %s: %w", manifestPath, err)
	}

	log.Printf("🗂️  Updated cluster registry (%d cluster(s)) at %s", len(manifest), manifestPath)
	return nil
}
