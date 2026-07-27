package helpers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"kubendt/kubeclient"
)

// MaxNamespaceFileBytes mirrors helpers.MaxMountFileBytes: files larger than
// this cannot be safely mounted into a pod because ConfigMaps cap at ~1 MiB
// in etcd. We reject early at upload time so the user gets feedback before
// trying to deploy.
const MaxNamespaceFileBytes = 1 << 20

// validateMountableContent enforces the two invariants that ConfigMap-backed
// mounts impose: size <= 1 MiB and valid UTF-8 (because ConfigMap.Data is a
// Go string field that silently corrupts binary content). Returns an error
// suitable for surfacing to the user in the file manager UI.
func validateMountableContent(filename string, data []byte) error {
	if len(data) > MaxNamespaceFileBytes {
		return fmt.Errorf("file %q is %d bytes; namespace files are limited to %d bytes (ConfigMaps cap at ~1 MiB in etcd). Split the file or use a different mechanism", filename, len(data), MaxNamespaceFileBytes)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("file %q contains non-UTF-8 bytes; only text files are supported. ConfigMap.Data is a string field; binaries would corrupt silently. If you need a binary, use a different mechanism", filename)
	}
	return nil
}

// FileTreeNode represents a node in the file tree (file or folder).
// Sensitive is set on file leaves whose namespace_file_meta row has
// sensitive=1; folders never carry the flag.
type FileTreeNode struct {
	Name      string         `json:"name"`
	IsDir     bool           `json:"isDir"`
	Path      string         `json:"path"` // path relative to the namespace
	Sensitive bool           `json:"sensitive,omitempty"`
	Children  []FileTreeNode `json:"children,omitempty"`
}

func filesBasePath() string {
	if basePath := os.Getenv("FILES_BASE_PATH"); basePath != "" {
		return basePath
	}
	return "files"
}

// namespaceFilesDir returns the on-disk root for a namespace's files, scoped to
// the active cluster: <base>/<clusterID>/<namespace>. Scoping by cluster keeps
// identically-named namespaces in different clusters from sharing (and
// clobbering) each other's files.
func namespaceFilesDir(namespace string) (string, error) {
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return "", fmt.Errorf("cannot resolve files path: %w", err)
	}
	return filepath.Join(filesBasePath(), clusterID, namespace), nil
}

// validatePath prevents path traversal attacks
func validatePath(basePath, fullPath string) error {
	// Resolve absolute paths to prevent ../ escapes
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return fmt.Errorf("error processing base path: %w", err)
	}
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return fmt.Errorf("error processing path: %w", err)
	}
	// Ensure fullPath is inside basePath
	if !strings.HasPrefix(absFull, absBase) {
		return fmt.Errorf("access denied: path outside namespace")
	}
	return nil
}

// ListFiles returns a hierarchical file/folder structure with the
// sensitive flag attached to each leaf (file) based on the namespace_file_meta
// table. Folders never carry the flag.
func ListFiles(namespace string) ([]FileTreeNode, error) {
	path, err := namespaceFilesDir(namespace)
	if err != nil {
		return nil, err
	}

	// Create folder if it does not exist
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, fmt.Errorf("error creating namespace folder %s: %w", namespace, err)
	}

	meta, _ := ListFileMetaForNamespace(namespace)

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("error reading namespace folder %s: %w", namespace, err)
	}

	var result []FileTreeNode
	for _, entry := range entries {
		node := FileTreeNode{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Path:  entry.Name(),
		}
		if entry.IsDir() {
			// Recursively get subfolder contents
			children, err := listFilesRecursive(filepath.Join(path, entry.Name()), entry.Name(), meta)
			if err == nil {
				node.Children = children
			}
		} else if m, ok := meta[entry.Name()]; ok {
			node.Sensitive = m.Sensitive
		}
		result = append(result, node)
	}

	if result == nil {
		result = []FileTreeNode{}
	}

	return result, nil
}

// listFilesRecursive recursively traverses subfolders
func listFilesRecursive(dirPath, relPath string, meta map[string]FileMeta) ([]FileTreeNode, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var result []FileTreeNode
	for _, entry := range entries {
		fullRelPath := filepath.Join(relPath, entry.Name())
		node := FileTreeNode{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Path:  fullRelPath,
		}
		if entry.IsDir() {
			children, err := listFilesRecursive(filepath.Join(dirPath, entry.Name()), fullRelPath, meta)
			if err == nil {
				node.Children = children
			}
		} else if m, ok := meta[fullRelPath]; ok {
			node.Sensitive = m.Sensitive
		}
		result = append(result, node)
	}

	if result == nil {
		result = []FileTreeNode{}
	}

	return result, nil
}

// NamespaceFileExists reports whether filename exists as a regular file in the
// namespace's (cluster-scoped) file directory. Used for pre-deploy validation
// of mount references. Path traversal is rejected the same way as reads.
func NamespaceFileExists(namespace, filename string) (bool, error) {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return false, err
	}
	fullPath := filepath.Join(basePath, filename)
	if err := validatePath(basePath, fullPath); err != nil {
		return false, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("could not stat file %s: %w", filename, err)
	}
	return !info.IsDir(), nil
}

// ResolveMountedFileName maps a mounted volume's SubPath data key back to the
// original file-manager path and reports whether that file still exists.
//
// Mounts store the file under a sanitized ConfigMap/Secret data key where path
// separators are collapsed to "_" (see SanitizeConfigMapDataKey), so a file in
// a subfolder like "web-server/index.html" appears in the pod spec only as
// "web-server_index.html". The mangling is not reversible in isolation (a real
// file could legitimately contain "_"), so we walk the file manager and return
// the path whose sanitized key matches. When nothing matches (the source file
// was deleted) the sanitized key is returned as-is with exists=false, so
// callers can flag the mount as missing.
func ResolveMountedFileName(namespace, dataKey string) (path string, exists bool) {
	baseDir, err := namespaceFilesDir(namespace)
	if err != nil {
		return dataKey, false
	}
	match := ""
	_ = filepath.WalkDir(baseDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(baseDir, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if SanitizeConfigMapDataKey(rel) == dataKey {
			match = rel
			return filepath.SkipAll
		}
		return nil
	})
	if match == "" {
		return dataKey, false
	}
	return match, true
}

// GetFileContent gets file content (supports nested subfolder paths)
func GetFileContent(namespace, filename string) ([]byte, error) {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return nil, err
	}
	fullPath := filepath.Join(basePath, filename)

	// Security validation
	if err := validatePath(basePath, fullPath); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("could not read file %s: %w", filename, err)
	}

	return data, nil
}

// UploadFile creates a file (supports nested subfolder paths). The full
// content is buffered to validate size and UTF-8 before writing, rejecting
// in-memory keeps a half-written file from ever landing on disk.
func UploadFile(namespace, filename string, file multipart.File) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(basePath, filename)

	// Security validation
	if err := validatePath(basePath, fullPath); err != nil {
		return err
	}

	// Buffer with a hard cap (limit + 1 so we can distinguish "exactly 1 MiB"
	// from "more than 1 MiB"). Anything larger is rejected before disk.
	buf := bytes.NewBuffer(nil)
	if _, err := io.Copy(buf, io.LimitReader(file, MaxNamespaceFileBytes+1)); err != nil {
		return fmt.Errorf("could not read upload: %w", err)
	}
	data := buf.Bytes()
	if err := validateMountableContent(filename, data); err != nil {
		return err
	}

	// Create parent directories if needed
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("could not create folder structure: %w", err)
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("could not save file: %w", err)
	}

	return nil
}

// UpdateFileContent updates file content (supports nested subfolder paths).
// Enforces the same size / UTF-8 invariants as UploadFile so the file stays
// safely mountable.
func UpdateFileContent(namespace, filename, newContent string) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(basePath, filename)

	// Security validation
	if err := validatePath(basePath, fullPath); err != nil {
		return err
	}

	// Ensure file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return fmt.Errorf("file '%s' does not exist in namespace '%s'", filename, namespace)
	}

	if err := validateMountableContent(filename, []byte(newContent)); err != nil {
		return err
	}

	if err := os.WriteFile(fullPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("could not write file: %w", err)
	}

	return nil
}

// DeleteFile deletes a file (supports nested subfolder paths)
func DeleteFile(namespace, filename string) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(basePath, filename)

	// Security validation
	if err := validatePath(basePath, fullPath); err != nil {
		return err
	}

	// Ensure file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return fmt.Errorf("file '%s' does not exist in namespace '%s'", filename, namespace)
	}

	if err := os.Remove(fullPath); err != nil {
		return fmt.Errorf("could not delete file: %w", err)
	}

	return nil
}

// CreateDirectory creates a folder (supports nested subfolders)
func CreateDirectory(namespace, folderPath string) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(basePath, folderPath)

	// Security validation
	if err := validatePath(basePath, fullPath); err != nil {
		return err
	}

	// Ensure destination does not already exist
	if _, err := os.Stat(fullPath); err == nil {
		return fmt.Errorf("folder '%s' already exists", folderPath)
	}

	if err := os.MkdirAll(fullPath, 0755); err != nil {
		return fmt.Errorf("could not create folder: %w", err)
	}

	return nil
}

// DeleteFolder deletes a folder (recursively)
func DeleteFolder(namespace, folderPath string) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(basePath, folderPath)

	// Security validation
	if err := validatePath(basePath, fullPath); err != nil {
		return err
	}

	// Ensure folder exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return fmt.Errorf("folder '%s' does not exist", folderPath)
	}

	// RemoveAll deletes recursively
	if err := os.RemoveAll(fullPath); err != nil {
		return fmt.Errorf("could not delete folder: %w", err)
	}

	return nil
}

// ExtractZip extracts a .zip file into the namespace
func ExtractZip(namespace string, zipFile multipart.File) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}

	// Create base directory if needed
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return fmt.Errorf("could not create base folder: %w", err)
	}

	// Read zip file bytes
	zipBytes := make([]byte, 0)
	buffer := make([]byte, 512)
	for {
		n, err := zipFile.Read(buffer)
		if n > 0 {
			zipBytes = append(zipBytes, buffer[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading zip file: %w", err)
		}
	}

	// Open zip from bytes
	reader, err := zip.NewReader(strings.NewReader(string(zipBytes)), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("error opening zip file: %w", err)
	}

	// Extract each file
	for _, file := range reader.File {
		fullPath := filepath.Join(basePath, file.Name)

		// Validate path (prevent path traversal)
		if err := validatePath(basePath, fullPath); err != nil {
			continue // Skip files with invalid paths
		}

		// If directory
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				return fmt.Errorf("error creating directory: %w", err)
			}
		} else {
			// Create parent directory
			dir := filepath.Dir(fullPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("error creating parent directory: %w", err)
			}

			// Open source zip entry
			src, err := file.Open()
			if err != nil {
				return fmt.Errorf("error opening file inside zip: %w", err)
			}
			defer src.Close()

			// Create destination file
			dst, err := os.Create(fullPath)
			if err != nil {
				return fmt.Errorf("error creating destination file: %w", err)
			}
			defer dst.Close()

			// Copy content
			if _, err := io.Copy(dst, src); err != nil {
				return fmt.Errorf("error copying content: %w", err)
			}

			// Preserve zip entry permissions
			os.Chmod(fullPath, file.Mode())
		}
	}

	return nil
}

// RenameFile renames a file or folder (supports nested paths)
func RenameFile(namespace, oldPath, newPath string) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}
	oldFullPath := filepath.Join(basePath, oldPath)
	newFullPath := filepath.Join(basePath, newPath)

	// Security validation
	if err := validatePath(basePath, oldFullPath); err != nil {
		return err
	}
	if err := validatePath(basePath, newFullPath); err != nil {
		return err
	}

	// Ensure source exists
	if _, err := os.Stat(oldFullPath); os.IsNotExist(err) {
		return fmt.Errorf("'%s' does not exist", oldPath)
	}

	// Ensure destination does not exist
	if _, err := os.Stat(newFullPath); err == nil {
		return fmt.Errorf("'%s' already exists", newPath)
	}

	// Create parent directory if needed
	dir := filepath.Dir(newFullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating directory: %w", err)
	}

	// Rename
	if err := os.Rename(oldFullPath, newFullPath); err != nil {
		return fmt.Errorf("error renaming: %w", err)
	}

	return nil
}

// ExportAsZip streams the namespace as a ZIP archive into out.
func ExportAsZip(namespace string, out io.Writer) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}

	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return fmt.Errorf("namespace '%s' does not exist", namespace)
	}

	w := zip.NewWriter(out)

	var addFilesToZip func(string, string) error
	addFilesToZip = func(dirPath, zipPrefix string) error {
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			fullPath := filepath.Join(dirPath, entry.Name())
			zipPath := filepath.Join(zipPrefix, entry.Name())
			zipPath = strings.ReplaceAll(zipPath, "\\", "/")

			if entry.IsDir() {
				if err := addFilesToZip(fullPath, zipPath); err != nil {
					return err
				}
				continue
			}

			f, err := w.Create(zipPath)
			if err != nil {
				return err
			}
			src, err := os.Open(fullPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, src); err != nil {
				src.Close()
				return err
			}
			src.Close()
		}
		return nil
	}

	if err := addFilesToZip(basePath, ""); err != nil {
		return fmt.Errorf("error creating zip: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("error closing zip: %w", err)
	}

	return nil
}

// ExtractTarGz extracts a .tar.gz file into the namespace
func ExtractTarGz(namespace string, tarGzFile multipart.File) error {
	basePath, err := namespaceFilesDir(namespace)
	if err != nil {
		return err
	}

	// Create base directory if needed
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return fmt.Errorf("could not create base folder: %w", err)
	}

	// Open gzip reader
	gzReader, err := gzip.NewReader(tarGzFile)
	if err != nil {
		return fmt.Errorf("error opening gzip: %w", err)
	}
	defer gzReader.Close()

	// Read tar stream
	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %w", err)
		}

		fullPath := filepath.Join(basePath, header.Name)

		// Validate path (prevent path traversal)
		if err := validatePath(basePath, fullPath); err != nil {
			continue // Skip files with invalid paths
		}

		// If directory
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(fullPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("error creating directory: %w", err)
			}
		} else {
			// Create parent directory
			dir := filepath.Dir(fullPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("error creating parent directory: %w", err)
			}

			// Create file
			dst, err := os.Create(fullPath)
			if err != nil {
				return fmt.Errorf("error creating file: %w", err)
			}
			defer dst.Close()

			// Copy content
			if _, err := io.Copy(dst, tarReader); err != nil {
				return fmt.Errorf("error copying content: %w", err)
			}

			// Preserve tar permissions
			os.Chmod(fullPath, os.FileMode(header.Mode))
		}
	}

	return nil
}
