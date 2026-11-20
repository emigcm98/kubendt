package helpers

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kubendt/kubeclient"

	_ "github.com/mattn/go-sqlite3"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// The file store against client-go's fake clientset, with the disk an older
// backend would have used pointed at an empty temp folder.
func fakeFileStore(t *testing.T, objs ...runtime.Object) *fake.Clientset {
	t.Helper()
	cs := fake.NewClientset(objs...)
	prevCM, prevS, prevSTS, prevDB, prevReady := fileConfigMaps, fileSecrets, fileStatefulSets, DB, fileStoreReady
	fileStoreReady = func() error { return nil }
	fileConfigMaps = func(ns string) corev1.ConfigMapInterface { return cs.CoreV1().ConfigMaps(ns) }
	fileSecrets = func(ns string) corev1.SecretInterface { return cs.CoreV1().Secrets(ns) }
	fileStatefulSets = func(ns string) appsv1client.StatefulSetInterface { return cs.AppsV1().StatefulSets(ns) }
	DB = nil
	t.Setenv("FILES_BASE_PATH", t.TempDir())
	kubeclient.SetClusterIDForTest("c1")
	migratedNamespaces.Range(func(k, _ any) bool { migratedNamespaces.Delete(k); return true })
	t.Cleanup(func() {
		fileConfigMaps, fileSecrets, fileStatefulSets, DB, fileStoreReady = prevCM, prevS, prevSTS, prevDB, prevReady
		kubeclient.SetClusterIDForTest("")
	})
	return cs
}

type multipartFile struct{ *bytes.Reader }

func (multipartFile) Close() error { return nil }

func upload(t *testing.T, ns, p, content string) {
	t.Helper()
	var f multipart.File = multipartFile{bytes.NewReader([]byte(content))}
	if err := UploadFile(ns, p, f); err != nil {
		t.Fatalf("upload %s: %v", p, err)
	}
}

func TestFilesAreObjectsOfTheNamespace(t *testing.T) {
	cs := fakeFileStore(t)
	upload(t, "lab", "web/index.html", "<h1>hi</h1>")
	upload(t, "lab", "test.sh", "echo hi")
	cm, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-file-web-index-html", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cm.Labels[FileKindLabel] != fileKind || cm.Labels[legacyMountLabel] != "true" || cm.Annotations[MountFilePathAnnotation] != "web/index.html" || cm.Data["web_index.html"] != "<h1>hi</h1>" {
		t.Fatalf("the object should carry the kind, the path and the content under the mount key: %v %v %v", cm.Labels, cm.Annotations, cm.Data)
	}
	tree, err := ListFiles("lab")
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || !tree[0].IsDir || tree[0].Path != "web" || tree[0].Children[0].Path != "web/index.html" || tree[1].Name != "test.sh" {
		t.Fatalf("the tree should show the folder first with its file, then the file: %+v", tree)
	}
	if data, err := GetFileContent("lab", "test.sh"); err != nil || string(data) != "echo hi" {
		t.Fatalf("read: %q %v", data, err)
	}
	if err := UpdateFileContent("lab", "test.sh", "echo bye"); err != nil {
		t.Fatal(err)
	}
	if data, _ := GetFileContent("lab", "test.sh"); string(data) != "echo bye" {
		t.Fatalf("update: %q", data)
	}
	if err := UpdateFileContent("lab", "nope.txt", "x"); err == nil {
		t.Error("updating a file that is not there must fail")
	}
	if ok, _ := NamespaceFileExists("lab", "test.sh"); !ok {
		t.Error("the file exists")
	}
	if index := BuildMountedFileIndex("lab"); index["web_index.html"] != "web/index.html" {
		t.Errorf("the index maps the mount key back to the path: %v", index)
	}
	name, secret, err := CreateMountResourceForFile("lab", "test.sh")
	if err != nil || secret || name != "kubendt-file-test-sh" {
		t.Fatalf("a mount references the file's own object: %s %v %v", name, secret, err)
	}
	if _, _, err := CreateMountResourceForFile("lab", "missing.txt"); err == nil || !strings.Contains(err.Error(), ErrFileMissing.Error()) {
		t.Fatalf("a mount of a file that is not there: %v", err)
	}
	if err := UploadFile("lab", "../escape", multipartFile{bytes.NewReader([]byte("x"))}); err == nil {
		t.Error("a path climbing out must be refused")
	}
	if err := UploadFile("lab", "bad name.txt", multipartFile{bytes.NewReader([]byte("x"))}); err == nil {
		t.Error("a name that cannot be a mount key must be refused")
	}
	if err := UploadFile("lab", "bin", multipartFile{bytes.NewReader([]byte{0xff, 0xfe})}); err == nil {
		t.Error("binary content must be refused")
	}
}

func TestSensitiveFilesAreSecretsAndFlipKinds(t *testing.T) {
	cs := fakeFileStore(t)
	upload(t, "lab", "secret.env", "TOKEN=1")
	if err := SetFileSensitive("lab", "secret.env", true); err != nil {
		t.Fatal(err)
	}
	s, err := cs.CoreV1().Secrets("lab").Get(context.TODO(), "kubendt-secret-file-secret-env", metav1.GetOptions{})
	if err != nil || string(s.Data["secret.env"]) != "TOKEN=1" || s.Labels[sensitiveLabel] != "true" || s.Labels[FileKindLabel] != fileKind {
		t.Fatalf("a sensitive file is a Secret with the content: %v %v", s, err)
	}
	if _, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-file-secret-env", metav1.GetOptions{}); err == nil {
		t.Error("the ConfigMap of a file nobody mounts goes when it becomes a Secret")
	}
	if meta, _ := GetFileMeta("lab", "secret.env"); !meta.Sensitive {
		t.Error("the flag reads off the object")
	}
	tree, _ := ListFiles("lab")
	if len(tree) != 1 || !tree[0].Sensitive {
		t.Errorf("the tree flags it: %+v", tree)
	}
	// An upload keeps the kind.
	upload(t, "lab", "secret.env", "TOKEN=2")
	if data, _ := GetFileContent("lab", "secret.env"); string(data) != "TOKEN=2" {
		t.Errorf("the Secret took the upload: %q", data)
	}
	name, secret, _ := CreateMountResourceForFile("lab", "secret.env")
	if !secret || name != "kubendt-secret-file-secret-env" {
		t.Errorf("a mount references the Secret: %s %v", name, secret)
	}
	if err := SetFileSensitive("lab", "secret.env", false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-file-secret-env", metav1.GetOptions{}); err != nil {
		t.Error("back to a ConfigMap")
	}
}

func mountingStatefulSet(ns, name, cmName, secretName string) *appsv1.StatefulSet {
	vols := []v1.Volume{}
	if cmName != "" {
		vols = append(vols, v1.Volume{Name: "f", VolumeSource: v1.VolumeSource{ConfigMap: &v1.ConfigMapVolumeSource{LocalObjectReference: v1.LocalObjectReference{Name: cmName}}}})
	}
	if secretName != "" {
		vols = append(vols, v1.Volume{Name: "s", VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: secretName}}})
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	sts.Spec.Template.Spec.Volumes = vols
	return sts
}

func TestDeletedFilesStayForThePodsThatMountThem(t *testing.T) {
	cs := fakeFileStore(t, mountingStatefulSet("lab", "pc", "kubendt-file-test-sh", ""))
	upload(t, "lab", "test.sh", "echo hi")
	upload(t, "lab", "loose.txt", "x")
	if err := DeleteFile("lab", "test.sh"); err != nil {
		t.Fatal(err)
	}
	cm, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-file-test-sh", metav1.GetOptions{})
	if err != nil || cm.Labels[FileKindLabel] != orphanKind || cm.Data["test.sh"] != "echo hi" {
		t.Fatalf("a mounted file deleted stays as a mount with its content: %v %v", cm, err)
	}
	if tree, _ := ListFiles("lab"); len(tree) != 1 || tree[0].Name != "loose.txt" {
		t.Errorf("the manager no longer lists it: %+v", tree)
	}
	if ok, _ := NamespaceFileExists("lab", "test.sh"); ok {
		t.Error("a mount is not a file")
	}
	if IsFileObject(cm.Labels) {
		t.Error("the garbage collection may take a mount")
	}
	if err := DeleteFile("lab", "loose.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-file-loose-txt", metav1.GetOptions{}); err == nil {
		t.Error("a file nobody mounts goes for good")
	}
	// An upload of the same path takes the mount over: the pods get the new
	// content on their next restart, as they always did.
	upload(t, "lab", "test.sh", "echo again")
	cm, _ = cs.CoreV1().ConfigMaps("lab").Get(context.TODO(), "kubendt-file-test-sh", metav1.GetOptions{})
	if cm.Labels[FileKindLabel] != fileKind || cm.Data["test.sh"] != "echo again" {
		t.Errorf("the mount became the file again: %v %v", cm.Labels, cm.Data)
	}
}

func TestFoldersRenamesAndArchives(t *testing.T) {
	fakeFileStore(t)
	if err := CreateDirectory("lab", "empty"); err != nil {
		t.Fatal(err)
	}
	if err := CreateDirectory("lab", "empty"); err == nil {
		t.Error("a folder that exists")
	}
	upload(t, "lab", "web/a.txt", "a")
	upload(t, "lab", "web/sub/b.txt", "b")
	tree, _ := ListFiles("lab")
	if len(tree) != 2 || tree[0].Path != "empty" || !tree[0].IsDir || len(tree[0].Children) != 0 || tree[1].Path != "web" {
		t.Fatalf("an empty folder shows, then web: %+v", tree)
	}
	if err := RenameFile("lab", "web", "site"); err != nil {
		t.Fatal(err)
	}
	if data, err := GetFileContent("lab", "site/sub/b.txt"); err != nil || string(data) != "b" {
		t.Fatalf("a renamed folder moves its files: %q %v", data, err)
	}
	if ok, _ := NamespaceFileExists("lab", "web/a.txt"); ok {
		t.Error("the old path is gone")
	}
	if err := RenameFile("lab", "site/a.txt", "site/sub/b.txt"); err == nil {
		t.Error("renaming onto a file that exists must fail")
	}
	if err := RenameFile("lab", "site/a.txt", "top.txt"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ExportAsZip("lab", &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, "site/sub/b.txt") || !strings.Contains(joined, "top.txt") || !strings.Contains(joined, "empty/") {
		t.Errorf("the archive holds the files and the empty folder: %v", names)
	}
	if err := DeleteFolder("lab", "site"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteFolder("lab", "site"); err == nil {
		t.Error("a folder that is gone")
	}
	// The archive comes back into another namespace, binary entries skipped.
	buf.Reset()
	w := zip.NewWriter(&buf)
	e, _ := w.Create("conf/x.yaml")
	_, _ = e.Write([]byte("a: 1"))
	e, _ = w.Create("blob.bin")
	_, _ = e.Write([]byte{0xff, 0x00})
	_ = w.Close()
	report, err := ExtractZip("other", multipartFile{bytes.NewReader(buf.Bytes())})
	if err != nil || report.Imported != 1 || len(report.Skipped) != 1 {
		t.Fatalf("import: %+v %v", report, err)
	}
	if data, _ := GetFileContent("other", "conf/x.yaml"); string(data) != "a: 1" {
		t.Errorf("imported: %q", data)
	}
	if err := DeleteNamespaceFilesAll("other"); err != nil {
		t.Fatal(err)
	}
	if NamespaceHasFiles("other") {
		t.Error("nothing left")
	}
}

func TestFilesMigrateFromDisk(t *testing.T) {
	fakeFileStore(t)
	dir := filepath.Join(os.Getenv("FILES_BASE_PATH"), "c1", "old")
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "web", "index.html"), []byte("<p>"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "secret.env"), []byte("K=v"), 0o644)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skip("no sqlite:", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE namespace_file_meta (cluster_id TEXT, namespace TEXT, path TEXT, sensitive INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO namespace_file_meta VALUES ('c1', 'old', 'secret.env', 1)`); err != nil {
		t.Fatal(err)
	}
	DB = db

	tree, err := ListFiles("old")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	var walk func([]FileTreeNode)
	walk = func(nodes []FileTreeNode) {
		for _, n := range nodes {
			paths[n.Path] = n.Sensitive
			walk(n.Children)
		}
	}
	walk(tree)
	if _, ok := paths["web/index.html"]; !ok || !paths["secret.env"] || paths["empty"] {
		t.Fatalf("the disk files should be objects now, the flag from the row, the empty folder a marker: %v", paths)
	}
	if _, err := os.Stat(filepath.Join(dir, migratedMarker)); err != nil {
		t.Error("the disk gets the marker")
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM namespace_file_meta`).Scan(&left); err != nil || left != 0 {
		t.Errorf("the rows go once moved, %d left (%v)", left, err)
	}
	// The next listing reads the cluster only: a file dropped from the
	// manager does not come back from the disk.
	if err := DeleteFile("old", "web/index.html"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := NamespaceFileExists("old", "web/index.html"); ok {
		t.Error("deleted stays deleted")
	}
}
