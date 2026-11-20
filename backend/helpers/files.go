package helpers

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"kubendt/kubeclient"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// A namespace's files are ConfigMaps, or Secrets when flagged sensitive, in
// the namespace itself: one object per file, named after the path, with the
// path in an annotation and the content under one key. The same object is
// what a pod mounts, so the file manager and the mounts never disagree, and
// any backend sees the files. A folder with nothing in it is a marker
// ConfigMap. Files an older backend kept on disk under FILES_BASE_PATH move
// here the first time the namespace is touched.

// MaxNamespaceFileBytes is what a ConfigMap or Secret holds in etcd; a file
// is rejected at upload time, before any deploy.
const MaxNamespaceFileBytes = 1 << 20

const (
	FileKindLabel    = "kubendt/kind"
	fileKind         = "file"
	folderKind       = "folder"
	orphanKind       = "mount"
	sensitiveLabel   = "kubendt/sensitive"
	legacyMountLabel = "kubendt/mount-file"
	migratedMarker   = ".kubendt-migrated"
)

var (
	// ErrFileMissing says a mount names a file the namespace does not hold.
	ErrFileMissing = errors.New("file not in the namespace file manager")
	dataKeyRe      = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
)

// fileStoreReady refuses the store before a cluster is configured, so a plan
// that reads a file off the cluster fails with a message, not a nil client.
var fileStoreReady = func() error {
	if kubeclient.Clientset == nil {
		return fmt.Errorf("no Kubernetes cluster is configured, the namespace files live there")
	}
	return nil
}

// The clients the store goes through, variables so the tests can hand them
// a fake clientset.
var (
	fileConfigMaps = func(namespace string) corev1.ConfigMapInterface {
		return kubeclient.Clientset.CoreV1().ConfigMaps(namespace)
	}
	fileSecrets = func(namespace string) corev1.SecretInterface {
		return kubeclient.Clientset.CoreV1().Secrets(namespace)
	}
	fileStatefulSets = func(namespace string) appsv1client.StatefulSetInterface {
		return kubeclient.Clientset.AppsV1().StatefulSets(namespace)
	}
)

// validateMountableContent enforces what a ConfigMap or Secret can hold: the
// size, and UTF-8 since Data is a string that would corrupt bytes silently.
func validateMountableContent(filename string, data []byte) error {
	if len(data) > MaxNamespaceFileBytes {
		return fmt.Errorf("file %q is %d bytes; namespace files are limited to %d bytes (ConfigMaps cap at ~1 MiB in etcd). Split the file or use a different mechanism", filename, len(data), MaxNamespaceFileBytes)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("file %q contains non-UTF-8 bytes; only text files are supported. ConfigMap.Data is a string field; binaries would corrupt silently. If you need a binary, use a different mechanism", filename)
	}
	return nil
}

// cleanFilePath is the path as the store keys it: slash-separated, relative,
// never climbing out, and mountable as a ConfigMap key once sanitized.
func cleanFilePath(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", fmt.Errorf("access denied: path outside namespace")
		}
	}
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" || p == "." {
		return "", fmt.Errorf("a file needs a name")
	}
	if !dataKeyRe.MatchString(SanitizeConfigMapDataKey(p)) {
		return "", fmt.Errorf("file name %q cannot be mounted: only letters, digits, '-', '_' and '.' are allowed in each part", p)
	}
	return p, nil
}

// FileTreeNode is one entry of the file manager's tree.
type FileTreeNode struct {
	Name      string         `json:"name"`
	IsDir     bool           `json:"isDir"`
	Path      string         `json:"path"` // path relative to the namespace
	Sensitive bool           `json:"sensitive,omitempty"`
	Children  []FileTreeNode `json:"children,omitempty"`
}

// namespaceFile is a file as the store holds it.
type namespaceFile struct {
	Path      string
	Name      string // the object's name
	Sensitive bool   // a Secret
	Content   []byte
	// Mount says the object backs mounts only: the file was deleted or
	// renamed while pods used it, and it stays for them until no
	// StatefulSet references it.
	Mount bool
}

func fileLabels(sensitive bool) map[string]string {
	l := map[string]string{
		"app.kubernetes.io/managed-by": "kubendt-backend",
		FileKindLabel:                  fileKind,
		legacyMountLabel:               "true",
	}
	if sensitive {
		l[sensitiveLabel] = "true"
	}
	return l
}

func folderLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": "kubendt-backend", FileKindLabel: folderKind}
}

func folderMarkerName(folder string) string {
	return "kubendt-folder-" + sanitizeMountFileSuffix(folder)
}

// listNamespaceFiles reads every file object of the namespace, the orphaned
// mounts included when asked, keyed by path.
func listNamespaceFiles(namespace string, withMounts bool) (map[string]namespaceFile, error) {
	ctx := context.TODO()
	if err := fileStoreReady(); err != nil {
		return nil, err
	}
	out := map[string]namespaceFile{}
	cms, err := fileConfigMaps(namespace).List(ctx, metav1.ListOptions{LabelSelector: legacyMountLabel + "=true"})
	if err != nil {
		return nil, fmt.Errorf("could not list the files of %s: %w", namespace, err)
	}
	for i := range cms.Items {
		cm := &cms.Items[i]
		f := namespaceFile{Path: cm.Annotations[MountFilePathAnnotation], Name: cm.Name, Mount: cm.Labels[FileKindLabel] != fileKind}
		if f.Path == "" || (f.Mount && !withMounts) {
			continue
		}
		for _, v := range cm.Data {
			f.Content = []byte(v)
		}
		out[f.Path] = f
	}
	secrets, err := fileSecrets(namespace).List(ctx, metav1.ListOptions{LabelSelector: legacyMountLabel + "=true"})
	if err != nil {
		return nil, fmt.Errorf("could not list the sensitive files of %s: %w", namespace, err)
	}
	for i := range secrets.Items {
		s := &secrets.Items[i]
		f := namespaceFile{Path: s.Annotations[MountFilePathAnnotation], Name: s.Name, Sensitive: true, Mount: s.Labels[FileKindLabel] != fileKind}
		if f.Path == "" || (f.Mount && !withMounts) {
			continue
		}
		for _, v := range s.Data {
			f.Content = v
		}
		if existing, ok := out[f.Path]; !ok || existing.Mount && !f.Mount {
			out[f.Path] = f
		}
	}
	return out, nil
}

func listFolderMarkers(namespace string) (map[string]bool, error) {
	if err := fileStoreReady(); err != nil {
		return nil, err
	}
	cms, err := fileConfigMaps(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: FileKindLabel + "=" + folderKind})
	if err != nil {
		return nil, fmt.Errorf("could not list the folders of %s: %w", namespace, err)
	}
	out := map[string]bool{}
	for i := range cms.Items {
		if p := cms.Items[i].Annotations[MountFilePathAnnotation]; p != "" {
			out[p] = true
		}
	}
	return out, nil
}

// getNamespaceFile is one file by path, a Secret or a ConfigMap, the orphaned
// mount of the path included when asked.
func getNamespaceFile(namespace, p string, withMounts bool) (*namespaceFile, error) {
	ctx := context.TODO()
	if err := fileStoreReady(); err != nil {
		return nil, err
	}
	if s, err := fileSecrets(namespace).Get(ctx, SanitizeMountSecretName(p), metav1.GetOptions{}); err == nil {
		if s.Annotations[MountFilePathAnnotation] == p && (withMounts || s.Labels[FileKindLabel] == fileKind) {
			f := &namespaceFile{Path: p, Name: s.Name, Sensitive: true, Mount: s.Labels[FileKindLabel] != fileKind}
			for _, v := range s.Data {
				f.Content = v
			}
			if !f.Mount {
				return f, nil
			}
			if cm, err := fileConfigMaps(namespace).Get(ctx, SanitizeMountConfigMapName(p), metav1.GetOptions{}); err == nil && cm.Labels[FileKindLabel] == fileKind && cm.Annotations[MountFilePathAnnotation] == p {
				return configMapFile(cm), nil
			}
			return f, nil
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("could not read file %s: %w", p, err)
	}
	cm, err := fileConfigMaps(namespace).Get(ctx, SanitizeMountConfigMapName(p), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read file %s: %w", p, err)
	}
	if cm.Annotations[MountFilePathAnnotation] != p || (!withMounts && cm.Labels[FileKindLabel] != fileKind) {
		return nil, nil
	}
	return configMapFile(cm), nil
}

func configMapFile(cm *v1.ConfigMap) *namespaceFile {
	f := &namespaceFile{Path: cm.Annotations[MountFilePathAnnotation], Name: cm.Name, Mount: cm.Labels[FileKindLabel] != fileKind}
	for _, v := range cm.Data {
		f.Content = []byte(v)
	}
	return f
}

// writeNamespaceFile creates or replaces the object of a path with the
// content and kind asked. An orphaned mount of the path is taken over, the
// pods using it get the new content on their next restart, as they always
// did after an upload. The other kind's object of the path, if a file, goes
// the way a deleted file does.
func writeNamespaceFile(namespace, p string, content []byte, sensitive bool) error {
	ctx := context.TODO()
	if err := fileStoreReady(); err != nil {
		return err
	}
	key := SanitizeConfigMapDataKey(p)
	annotations := map[string]string{MountFilePathAnnotation: p}
	if sensitive {
		name := SanitizeMountSecretName(p)
		existing, err := fileSecrets(namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil:
			existing.Labels, existing.Annotations, existing.Data = fileLabels(true), annotations, map[string][]byte{key: content}
			if _, err := fileSecrets(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
				return fmt.Errorf("could not write file %s: %w", p, err)
			}
		case apierrors.IsNotFound(err):
			s := &v1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: fileLabels(true), Annotations: annotations}, Type: v1.SecretTypeOpaque, Data: map[string][]byte{key: content}}
			if _, err := fileSecrets(namespace).Create(ctx, s, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("could not write file %s: %w", p, err)
			}
		default:
			return fmt.Errorf("could not read file %s: %w", p, err)
		}
		return retireOtherKind(namespace, p, false)
	}
	name := SanitizeMountConfigMapName(p)
	existing, err := fileConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		existing.Labels, existing.Annotations, existing.Data = fileLabels(false), annotations, map[string]string{key: string(content)}
		if _, err := fileConfigMaps(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("could not write file %s: %w", p, err)
		}
	case apierrors.IsNotFound(err):
		cm := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: fileLabels(false), Annotations: annotations}, Data: map[string]string{key: string(content)}}
		if _, err := fileConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("could not write file %s: %w", p, err)
		}
	default:
		return fmt.Errorf("could not read file %s: %w", p, err)
	}
	return retireOtherKind(namespace, p, true)
}

// retireOtherKind removes a path's object of the kind it no longer is, or
// leaves it as a mount when pods use it.
func retireOtherKind(namespace, p string, secret bool) error {
	ctx := context.TODO()
	if secret {
		s, err := fileSecrets(namespace).Get(ctx, SanitizeMountSecretName(p), metav1.GetOptions{})
		if err != nil || s.Labels[FileKindLabel] != fileKind || s.Annotations[MountFilePathAnnotation] != p {
			return nil
		}
		return removeFileObject(namespace, &namespaceFile{Path: p, Name: s.Name, Sensitive: true})
	}
	cm, err := fileConfigMaps(namespace).Get(ctx, SanitizeMountConfigMapName(p), metav1.GetOptions{})
	if err != nil || cm.Labels[FileKindLabel] != fileKind || cm.Annotations[MountFilePathAnnotation] != p {
		return nil
	}
	return removeFileObject(namespace, &namespaceFile{Path: p, Name: cm.Name})
}

// mountedObjectNames is every ConfigMap and Secret name a StatefulSet of the
// namespace mounts.
func mountedObjectNames(namespace string) (cms, secrets map[string]bool, err error) {
	if err := fileStoreReady(); err != nil {
		return nil, nil, err
	}
	list, err := fileStatefulSets(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("could not list the StatefulSets of %s: %w", namespace, err)
	}
	cms, secrets = map[string]bool{}, map[string]bool{}
	for _, sts := range list.Items {
		for _, vol := range sts.Spec.Template.Spec.Volumes {
			if vol.ConfigMap != nil {
				cms[vol.ConfigMap.Name] = true
			}
			if vol.Secret != nil {
				secrets[vol.Secret.SecretName] = true
			}
		}
	}
	return cms, secrets, nil
}

// removeFileObject deletes a file's object, or keeps it as a mount when a
// StatefulSet still references it, so the pods keep what they mounted until
// they are redeployed and the garbage collection takes it then.
func removeFileObject(namespace string, f *namespaceFile) error {
	ctx := context.TODO()
	cms, secrets, err := mountedObjectNames(namespace)
	if err != nil {
		return err
	}
	if f.Sensitive {
		if secrets[f.Name] {
			s, err := fileSecrets(namespace).Get(ctx, f.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			s.Labels[FileKindLabel] = orphanKind
			_, err = fileSecrets(namespace).Update(ctx, s, metav1.UpdateOptions{})
			return err
		}
		if err := fileSecrets(namespace).Delete(ctx, f.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("could not delete file %s: %w", f.Path, err)
		}
		return nil
	}
	if cms[f.Name] {
		cm, err := fileConfigMaps(namespace).Get(ctx, f.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		cm.Labels[FileKindLabel] = orphanKind
		_, err = fileConfigMaps(namespace).Update(ctx, cm, metav1.UpdateOptions{})
		return err
	}
	if err := fileConfigMaps(namespace).Delete(ctx, f.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("could not delete file %s: %w", f.Path, err)
	}
	return nil
}

// IsFileObject says whether a ConfigMap or Secret is a file of the manager,
// which a clear or a node deletion must leave alone: only the mounts of
// files that are gone get collected with the StatefulSets that used them.
func IsFileObject(labels map[string]string) bool {
	return labels[FileKindLabel] == fileKind || labels[FileKindLabel] == folderKind
}

// ListFiles is the file manager's tree, folders first by name at each level.
func ListFiles(namespace string) ([]FileTreeNode, error) {
	if err := migrateNamespaceFiles(namespace); err != nil {
		return nil, err
	}
	files, err := listNamespaceFiles(namespace, false)
	if err != nil {
		return nil, err
	}
	folders, err := listFolderMarkers(namespace)
	if err != nil {
		return nil, err
	}
	type dir struct {
		files   map[string]namespaceFile
		subdirs map[string]*dir
	}
	newDir := func() *dir { return &dir{files: map[string]namespaceFile{}, subdirs: map[string]*dir{}} }
	root := newDir()
	ensure := func(folder string) *dir {
		d := root
		if folder == "" {
			return d
		}
		for _, seg := range strings.Split(folder, "/") {
			if d.subdirs[seg] == nil {
				d.subdirs[seg] = newDir()
			}
			d = d.subdirs[seg]
		}
		return d
	}
	for folder := range folders {
		ensure(folder)
	}
	for p, f := range files {
		folder := path.Dir(p)
		if folder == "." {
			folder = ""
		}
		ensure(folder).files[path.Base(p)] = f
	}
	var build func(d *dir, prefix string) []FileTreeNode
	build = func(d *dir, prefix string) []FileTreeNode {
		nodes := []FileTreeNode{}
		names := make([]string, 0, len(d.subdirs))
		for n := range d.subdirs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p := path.Join(prefix, n)
			nodes = append(nodes, FileTreeNode{Name: n, IsDir: true, Path: p, Children: build(d.subdirs[n], p)})
		}
		names = names[:0]
		for n := range d.files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			nodes = append(nodes, FileTreeNode{Name: n, Path: path.Join(prefix, n), Sensitive: d.files[n].Sensitive})
		}
		return nodes
	}
	return build(root, ""), nil
}

// NamespaceFileExists reports whether the namespace holds the file, a mount
// of a deleted file not counting.
func NamespaceFileExists(namespace, filename string) (bool, error) {
	p, err := cleanFilePath(filename)
	if err != nil {
		return false, err
	}
	f, err := getNamespaceFile(namespace, p, false)
	return f != nil, err
}

// BuildMountedFileIndex maps every file's sanitized data key back to its path,
// for mounts made before the path annotation existed.
func BuildMountedFileIndex(namespace string) map[string]string {
	index := map[string]string{}
	files, err := listNamespaceFiles(namespace, false)
	if err != nil {
		return index
	}
	for p := range files {
		index[SanitizeConfigMapDataKey(p)] = p
	}
	return index
}

// GetFileContent reads a file.
func GetFileContent(namespace, filename string) ([]byte, error) {
	p, err := cleanFilePath(filename)
	if err != nil {
		return nil, err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return nil, err
	}
	f, err := getNamespaceFile(namespace, p, false)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("could not read file %s: %w", filename, ErrFileMissing)
	}
	return f.Content, nil
}

// UploadFile creates or replaces a file. The content is read whole to check
// it before anything is written; an existing file keeps its kind.
func UploadFile(namespace, filename string, file multipart.File) error {
	p, err := cleanFilePath(filename)
	if err != nil {
		return err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return err
	}
	buf := bytes.NewBuffer(nil)
	if _, err := io.Copy(buf, io.LimitReader(file, MaxNamespaceFileBytes+1)); err != nil {
		return fmt.Errorf("could not read upload: %w", err)
	}
	data := buf.Bytes()
	if err := validateMountableContent(p, data); err != nil {
		return err
	}
	existing, err := getNamespaceFile(namespace, p, false)
	if err != nil {
		return err
	}
	return writeNamespaceFile(namespace, p, data, existing != nil && existing.Sensitive)
}

// UpdateFileContent replaces the content of a file that exists.
func UpdateFileContent(namespace, filename, newContent string) error {
	p, err := cleanFilePath(filename)
	if err != nil {
		return err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return err
	}
	existing, err := getNamespaceFile(namespace, p, false)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("file '%s' does not exist in namespace '%s'", filename, namespace)
	}
	if err := validateMountableContent(p, []byte(newContent)); err != nil {
		return err
	}
	return writeNamespaceFile(namespace, p, []byte(newContent), existing.Sensitive)
}

// DeleteFile removes a file; pods mounting it keep what they mounted.
func DeleteFile(namespace, filename string) error {
	p, err := cleanFilePath(filename)
	if err != nil {
		return err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return err
	}
	f, err := getNamespaceFile(namespace, p, false)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("file '%s' does not exist in namespace '%s'", filename, namespace)
	}
	return removeFileObject(namespace, f)
}

// CreateDirectory makes an empty folder, a marker until a file lands in it.
func CreateDirectory(namespace, folderPath string) error {
	p, err := cleanFilePath(folderPath)
	if err != nil {
		return err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return err
	}
	if exists, err := pathTaken(namespace, p); err != nil || exists {
		if err != nil {
			return err
		}
		return fmt.Errorf("folder '%s' already exists", folderPath)
	}
	cm := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: folderMarkerName(p), Namespace: namespace, Labels: folderLabels(), Annotations: map[string]string{MountFilePathAnnotation: p}}}
	if _, err := fileConfigMaps(namespace).Create(context.TODO(), cm, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create folder: %w", err)
	}
	return nil
}

// pathTaken says whether a file, a folder marker or files under the path exist.
func pathTaken(namespace, p string) (bool, error) {
	files, err := listNamespaceFiles(namespace, false)
	if err != nil {
		return false, err
	}
	for fp := range files {
		if fp == p || strings.HasPrefix(fp, p+"/") {
			return true, nil
		}
	}
	folders, err := listFolderMarkers(namespace)
	if err != nil {
		return false, err
	}
	for fp := range folders {
		if fp == p || strings.HasPrefix(fp, p+"/") {
			return true, nil
		}
	}
	return false, nil
}

// DeleteFolder removes a folder with everything under it.
func DeleteFolder(namespace, folderPath string) error {
	p, err := cleanFilePath(folderPath)
	if err != nil {
		return err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return err
	}
	files, err := listNamespaceFiles(namespace, false)
	if err != nil {
		return err
	}
	folders, err := listFolderMarkers(namespace)
	if err != nil {
		return err
	}
	found := false
	for fp, f := range files {
		if strings.HasPrefix(fp, p+"/") {
			found = true
			if err := removeFileObject(namespace, &f); err != nil {
				return err
			}
		}
	}
	for fp := range folders {
		if fp == p || strings.HasPrefix(fp, p+"/") {
			found = true
			if err := fileConfigMaps(namespace).Delete(context.TODO(), folderMarkerName(fp), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("could not delete folder: %w", err)
			}
		}
	}
	if !found {
		return fmt.Errorf("folder '%s' does not exist", folderPath)
	}
	return nil
}

// RenameFile moves a file, or a folder with everything under it. A renamed
// file is a new object, so pods mounting the old name keep it as a mount.
func RenameFile(namespace, oldPath, newPath string) error {
	from, err := cleanFilePath(oldPath)
	if err != nil {
		return err
	}
	to, err := cleanFilePath(newPath)
	if err != nil {
		return err
	}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return err
	}
	if taken, err := pathTaken(namespace, to); err != nil || taken {
		if err != nil {
			return err
		}
		return fmt.Errorf("'%s' already exists", newPath)
	}
	if f, err := getNamespaceFile(namespace, from, false); err != nil {
		return err
	} else if f != nil {
		if err := writeNamespaceFile(namespace, to, f.Content, f.Sensitive); err != nil {
			return err
		}
		return removeFileObject(namespace, f)
	}
	files, err := listNamespaceFiles(namespace, false)
	if err != nil {
		return err
	}
	folders, err := listFolderMarkers(namespace)
	if err != nil {
		return err
	}
	found := false
	for fp, f := range files {
		if !strings.HasPrefix(fp, from+"/") {
			continue
		}
		found = true
		if err := writeNamespaceFile(namespace, to+strings.TrimPrefix(fp, from), f.Content, f.Sensitive); err != nil {
			return err
		}
		if err := removeFileObject(namespace, &f); err != nil {
			return err
		}
	}
	for fp := range folders {
		if fp != from && !strings.HasPrefix(fp, from+"/") {
			continue
		}
		found = true
		if err := CreateDirectory(namespace, to+strings.TrimPrefix(fp, from)); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
		if err := fileConfigMaps(namespace).Delete(context.TODO(), folderMarkerName(fp), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if !found {
		return fmt.Errorf("'%s' does not exist", oldPath)
	}
	return nil
}

// ImportReport is what an archive import did.
type ImportReport struct {
	Imported int      `json:"imported"`
	Skipped  []string `json:"skipped,omitempty"`
}

// importEntry writes one archive entry, a folder or a file, and says whether
// it was skipped and why.
func importEntry(namespace, name string, isDir bool, content []byte) (skipped string, err error) {
	p, err := cleanFilePath(name)
	if err != nil {
		return fmt.Sprintf("%s: %v", name, err), nil
	}
	if isDir {
		if err := CreateDirectory(namespace, p); err != nil && !strings.Contains(err.Error(), "already exists") {
			return "", err
		}
		return "", nil
	}
	if err := validateMountableContent(p, content); err != nil {
		return err.Error(), nil
	}
	existing, err := getNamespaceFile(namespace, p, false)
	if err != nil {
		return "", err
	}
	return "", writeNamespaceFile(namespace, p, content, existing != nil && existing.Sensitive)
}

// ExtractZip imports a .zip into the namespace.
func ExtractZip(namespace string, zipFile multipart.File) (ImportReport, error) {
	report := ImportReport{}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return report, err
	}
	raw, err := io.ReadAll(zipFile)
	if err != nil {
		return report, fmt.Errorf("error reading zip file: %w", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return report, fmt.Errorf("error opening zip file: %w", err)
	}
	for _, file := range reader.File {
		var content []byte
		if !file.FileInfo().IsDir() {
			src, err := file.Open()
			if err != nil {
				return report, fmt.Errorf("error opening file inside zip: %w", err)
			}
			content, err = io.ReadAll(io.LimitReader(src, MaxNamespaceFileBytes+1))
			_ = src.Close()
			if err != nil {
				return report, fmt.Errorf("error reading file inside zip: %w", err)
			}
		}
		skipped, err := importEntry(namespace, file.Name, file.FileInfo().IsDir(), content)
		if err != nil {
			return report, err
		}
		if skipped != "" {
			report.Skipped = append(report.Skipped, skipped)
		} else if !file.FileInfo().IsDir() {
			report.Imported++
		}
	}
	return report, nil
}

// ExtractTarGz imports a .tar.gz or .tgz into the namespace.
func ExtractTarGz(namespace string, tarGzFile multipart.File) (ImportReport, error) {
	report := ImportReport{}
	if err := migrateNamespaceFiles(namespace); err != nil {
		return report, err
	}
	gzReader, err := gzip.NewReader(tarGzFile)
	if err != nil {
		return report, fmt.Errorf("error opening gzip: %w", err)
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return report, fmt.Errorf("error reading tar: %w", err)
		}
		isDir := header.Typeflag == tar.TypeDir
		var content []byte
		if !isDir {
			if header.Typeflag != tar.TypeReg {
				continue
			}
			content, err = io.ReadAll(io.LimitReader(tarReader, MaxNamespaceFileBytes+1))
			if err != nil {
				return report, fmt.Errorf("error reading file inside tar: %w", err)
			}
		}
		skipped, err := importEntry(namespace, header.Name, isDir, content)
		if err != nil {
			return report, err
		}
		if skipped != "" {
			report.Skipped = append(report.Skipped, skipped)
		} else if !isDir {
			report.Imported++
		}
	}
	return report, nil
}

// sortedFiles is the namespace's files by path, for the archives.
func sortedFiles(namespace string) ([]namespaceFile, map[string]bool, error) {
	if err := migrateNamespaceFiles(namespace); err != nil {
		return nil, nil, err
	}
	files, err := listNamespaceFiles(namespace, false)
	if err != nil {
		return nil, nil, err
	}
	folders, err := listFolderMarkers(namespace)
	if err != nil {
		return nil, nil, err
	}
	out := make([]namespaceFile, 0, len(files))
	for _, f := range files {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, folders, nil
}

// ExportAsZip streams the namespace's files as a zip.
func ExportAsZip(namespace string, out io.Writer) error {
	files, folders, err := sortedFiles(namespace)
	if err != nil {
		return err
	}
	w := zip.NewWriter(out)
	for folder := range folders {
		if _, err := w.Create(folder + "/"); err != nil {
			return fmt.Errorf("error creating zip: %w", err)
		}
	}
	for _, f := range files {
		entry, err := w.Create(f.Path)
		if err != nil {
			return fmt.Errorf("error creating zip: %w", err)
		}
		if _, err := entry.Write(f.Content); err != nil {
			return fmt.Errorf("error creating zip: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("error closing zip: %w", err)
	}
	return nil
}

// ExportAsTarGz streams the namespace's files as a gzip-compressed tar.
func ExportAsTarGz(namespace string, out io.Writer) error {
	files, folders, err := sortedFiles(namespace)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	now := metav1.Now().Time
	for folder := range folders {
		if err := tw.WriteHeader(&tar.Header{Name: folder + "/", Typeflag: tar.TypeDir, Mode: 0755, ModTime: now}); err != nil {
			return fmt.Errorf("error creating tar.gz: %w", err)
		}
	}
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f.Path, Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len(f.Content)), ModTime: now}); err != nil {
			return fmt.Errorf("error creating tar.gz: %w", err)
		}
		if _, err := tw.Write(f.Content); err != nil {
			return fmt.Errorf("error creating tar.gz: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("error closing tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("error closing gzip: %w", err)
	}
	return nil
}

// DeleteNamespaceFilesAll removes every file and folder of the namespace,
// pods keeping what they mount, and whatever an older backend left on disk.
func DeleteNamespaceFilesAll(namespace string) error {
	files, err := listNamespaceFiles(namespace, false)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := removeFileObject(namespace, &f); err != nil {
			return err
		}
	}
	folders, err := listFolderMarkers(namespace)
	if err != nil {
		return err
	}
	for fp := range folders {
		if err := fileConfigMaps(namespace).Delete(context.TODO(), folderMarkerName(fp), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return deleteLegacyFilesDir(namespace)
}

// NamespaceHasFiles says whether the namespace holds any file.
func NamespaceHasFiles(namespace string) bool {
	files, err := listNamespaceFiles(namespace, false)
	return err == nil && len(files) > 0
}

// The disk an older backend kept the files on, read once per namespace.

func filesBasePath() string {
	if basePath := os.Getenv("FILES_BASE_PATH"); basePath != "" {
		return basePath
	}
	return "files"
}

func legacyFilesDir(namespace string) (string, error) {
	clusterID, err := kubeclient.CurrentClusterID()
	if err != nil {
		return "", err
	}
	return filepath.Join(filesBasePath(), clusterID, namespace), nil
}

func deleteLegacyFilesDir(namespace string) error {
	dir, err := legacyFilesDir(namespace)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("could not remove the files directory %s: %w", dir, err)
	}
	return nil
}

// DeleteNamespaceFileDir removes the directory an older backend kept for the
// namespace when nothing but the migration marker is left in it.
func DeleteNamespaceFileDir(namespace string) error {
	dir, err := legacyFilesDir(namespace)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.Name() != migratedMarker {
			return nil
		}
	}
	return os.RemoveAll(dir)
}

var migratedNamespaces sync.Map

// migrateNamespaceFiles moves what an older backend kept on disk for the
// namespace into objects, once: the files with the sensitive flag their
// metadata row held, the empty folders as markers, the mounts an older
// deploy already made taken over as the files they were copies of. The disk
// keeps the files behind a marker, for the operator.
func migrateNamespaceFiles(namespace string) error {
	if _, done := migratedNamespaces.Load(namespace); done {
		return nil
	}
	dir, err := legacyFilesDir(namespace)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, migratedMarker)); err == nil {
		migratedNamespaces.Store(namespace, true)
		return nil
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		migratedNamespaces.Store(namespace, true)
		return nil
	}
	// The writes below go through the store, which would start this very
	// migration again: mark it running first, and undo that on failure.
	migratedNamespaces.Store(namespace, true)
	failed := true
	defer func() {
		if failed {
			migratedNamespaces.Delete(namespace)
		}
	}()
	meta, _ := ListFileMetaForNamespace(namespace)
	moved := 0
	walkErr := filepath.WalkDir(dir, func(fp string, d os.DirEntry, err error) error {
		if err != nil || fp == dir {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(fp, dir+string(os.PathSeparator)))
		if d.IsDir() {
			if entries, err := os.ReadDir(fp); err == nil && len(entries) == 0 {
				if err := CreateDirectory(namespace, rel); err != nil && !strings.Contains(err.Error(), "already exists") {
					return err
				}
			}
			return nil
		}
		p, err := cleanFilePath(rel)
		if err != nil {
			log.Printf("⚠️ Files of %s: %s left on disk, %v", namespace, rel, err)
			return nil
		}
		data, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		if err := validateMountableContent(p, data); err != nil {
			log.Printf("⚠️ Files of %s: %s left on disk, %v", namespace, rel, err)
			return nil
		}
		if err := writeNamespaceFile(namespace, p, data, meta[p].Sensitive); err != nil {
			return err
		}
		moved++
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("could not move the files of %s into the cluster: %w", namespace, walkErr)
	}
	if err := os.WriteFile(filepath.Join(dir, migratedMarker), []byte("the files live in the cluster now\n"), 0644); err != nil {
		return fmt.Errorf("could not mark the files of %s as moved: %w", namespace, err)
	}
	if err := DeleteAllFileMetaForNamespace(namespace); err != nil {
		log.Printf("⚠️ Files of %s: could not drop the metadata rows: %v", namespace, err)
	}
	failed = false
	if moved > 0 {
		log.Printf("ℹ️ Files of namespace '%s' moved from disk to the cluster (%d files)", namespace, moved)
	}
	return nil
}
