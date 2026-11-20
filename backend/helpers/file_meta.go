package helpers

import (
	"fmt"

	"kubendt/kubeclient"
)

// FileMeta is what a file carries beyond its content: whether it is a
// Secret rather than a ConfigMap. It is read off the object itself; the
// namespace_file_meta table only feeds the migration of an older backend's
// files from disk.
type FileMeta struct {
	Sensitive bool
}

// GetFileMeta reads a file's flags, zero for a file that is not there.
func GetFileMeta(namespace, p string) (FileMeta, error) {
	clean, err := cleanFilePath(p)
	if err != nil {
		return FileMeta{}, err
	}
	f, err := getNamespaceFile(namespace, clean, false)
	if err != nil || f == nil {
		return FileMeta{}, err
	}
	return FileMeta{Sensitive: f.Sensitive}, nil
}

// SetFileSensitive moves a file between a ConfigMap and a Secret. Pods that
// mount it keep the object they mount until they are redeployed, so the old
// one stays for them as a mount.
func SetFileSensitive(namespace, p string, sensitive bool) error {
	clean, err := cleanFilePath(p)
	if err != nil {
		return err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return err
	}
	f, err := getNamespaceFile(namespace, clean, false)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("file '%s' does not exist in namespace '%s'", p, namespace)
	}
	if f.Sensitive == sensitive {
		return nil
	}
	return writeNamespaceFile(namespace, clean, f.Content, sensitive)
}

// ListFileMetaForNamespace is the sensitive flags an older backend kept in
// SQLite, read by the migration.
func ListFileMetaForNamespace(namespace string) (map[string]FileMeta, error) {
	out := map[string]FileMeta{}
	if DB == nil {
		return out, nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return nil, err
	}
	rows, err := DB.Query(`SELECT path, sensitive FROM namespace_file_meta WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var sensitive int
		if err := rows.Scan(&p, &sensitive); err != nil {
			return nil, err
		}
		out[p] = FileMeta{Sensitive: sensitive == 1}
	}
	return out, rows.Err()
}

// DeleteAllFileMetaForNamespace drops the rows once the files have moved.
func DeleteAllFileMetaForNamespace(namespace string) error {
	if DB == nil {
		return nil
	}
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return err
	}
	_, err = DB.Exec(`DELETE FROM namespace_file_meta WHERE cluster_id = ? AND namespace = ?`, clusterID, namespace)
	return err
}
