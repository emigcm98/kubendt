package helpers

import (
	"database/sql"
	"fmt"

	"kubendt/kubeclient"
)

// FileMeta is per-file metadata stored in SQLite. Sensitive=true means the
// file is materialised as a Kubernetes Secret instead of a ConfigMap.
type FileMeta struct {
	Namespace string
	Path      string
	Sensitive bool
}

// GetFileMeta returns a zero-valued FileMeta (Sensitive=false) when no row exists.
func GetFileMeta(namespace, path string) (FileMeta, error) {
	meta := FileMeta{Namespace: namespace, Path: path}
	if DB == nil {
		return meta, nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return meta, err
	}
	var sensitive int
	err = DB.QueryRow(
		`SELECT sensitive FROM namespace_file_meta WHERE cluster_id = ? AND namespace = ? AND path = ?`,
		clusterID, namespace, path,
	).Scan(&sensitive)
	if err == sql.ErrNoRows {
		return meta, nil
	}
	if err != nil {
		return meta, fmt.Errorf("error reading file meta for %s/%s: %w", namespace, path, err)
	}
	meta.Sensitive = sensitive != 0
	return meta, nil
}

// ListFileMetaForNamespace returns a path -> meta map. Files not in the map
// are implicitly non-sensitive.
func ListFileMetaForNamespace(namespace string) (map[string]FileMeta, error) {
	out := make(map[string]FileMeta)
	if DB == nil {
		return out, nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return out, err
	}
	rows, err := DB.Query(
		`SELECT path, sensitive FROM namespace_file_meta WHERE cluster_id = ? AND namespace = ?`,
		clusterID, namespace,
	)
	if err != nil {
		return out, fmt.Errorf("error listing file meta for %s: %w", namespace, err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		var sensitive int
		if err := rows.Scan(&path, &sensitive); err != nil {
			return out, fmt.Errorf("error scanning file meta row: %w", err)
		}
		out[path] = FileMeta{Namespace: namespace, Path: path, Sensitive: sensitive != 0}
	}
	return out, nil
}

// SetFileSensitive upserts the sensitive flag for a file.
func SetFileSensitive(namespace, path string, sensitive bool) error {
	if DB == nil {
		return fmt.Errorf("database not initialised")
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	value := 0
	if sensitive {
		value = 1
	}
	_, err = DB.Exec(`
		INSERT INTO namespace_file_meta (cluster_id, namespace, path, sensitive)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(cluster_id, namespace, path) DO UPDATE SET
			sensitive = excluded.sensitive,
			updated_at = CURRENT_TIMESTAMP
	`, clusterID, namespace, path, value)
	if err != nil {
		return fmt.Errorf("error setting file meta for %s/%s: %w", namespace, path, err)
	}
	return nil
}

// DeleteFileMeta removes the metadata row for a file.
func DeleteFileMeta(namespace, path string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	_, err = DB.Exec(`DELETE FROM namespace_file_meta WHERE cluster_id = ? AND namespace = ? AND path = ?`, clusterID, namespace, path)
	if err != nil {
		return fmt.Errorf("error deleting file meta for %s/%s: %w", namespace, path, err)
	}
	return nil
}

// DeleteFileMetaByPrefix removes every metadata row whose path is under prefix.
func DeleteFileMetaByPrefix(namespace, prefix string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	likePrefix := escapeSQLiteLike(prefix) + "/%"
	if _, err := DB.Exec(
		`DELETE FROM namespace_file_meta WHERE cluster_id = ? AND namespace = ? AND (path = ? OR path LIKE ? ESCAPE '\')`,
		clusterID, namespace, prefix, likePrefix,
	); err != nil {
		return fmt.Errorf("error deleting file meta by prefix for %s/%s: %w", namespace, prefix, err)
	}
	return nil
}

// DeleteAllFileMetaForNamespace clears every metadata row for a namespace.
func DeleteAllFileMetaForNamespace(namespace string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	if _, err := DB.Exec(`DELETE FROM namespace_file_meta WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace); err != nil {
		return fmt.Errorf("error deleting all file meta for %s: %w", namespace, err)
	}
	return nil
}

// RenameFileMeta moves a metadata row from oldPath to newPath. No-op if absent.
func RenameFileMeta(namespace, oldPath, newPath string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	_, err = DB.Exec(`
		UPDATE namespace_file_meta
		   SET path = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE cluster_id = ? AND namespace = ? AND path = ?
	`, newPath, clusterID, namespace, oldPath)
	if err != nil {
		return fmt.Errorf("error renaming file meta from %s to %s in %s: %w", oldPath, newPath, namespace, err)
	}
	return nil
}

func escapeSQLiteLike(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' || c == '_' || c == '\\' {
			out = append(out, '\\')
		}
		out = append(out, c)
	}
	return string(out)
}
