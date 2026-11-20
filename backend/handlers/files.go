// handlers/files.go
package handlers

import (
	"io"
	"kubendt/helpers"
	"kubendt/types"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// GET /files/:namespace
func ListFiles(c *gin.Context) {
	namespace := c.Param("namespace")

	// Check namespace
	if err := helpers.ValidateNamespaceEnabled(namespace); err != nil {
		log.Printf("❌ Invalid namespace: %v", err)

		switch {
		case strings.Contains(err.Error(), "does not exist"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "is not enabled for KubeNDT"):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	files, err := helpers.ListFiles(namespace)
	if err != nil {
		log.Printf("❌ Error listing files for %s: %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"files": files})
}

func GetFileContent(c *gin.Context) {
	namespace := c.Param("namespace")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	data, err := helpers.GetFileContent(namespace, filename)
	if err != nil {
		log.Printf("❌ Error reading file %s in namespace %s: %v", filename, namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
}

func UploadFile(c *gin.Context) {
	namespace := c.Param("namespace")
	fileHeader, err := c.FormFile("file")
	if err != nil {
		log.Printf("❌ Error reading uploaded file: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "File not provided"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		log.Printf("❌ Error opening file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not open file"})
		return
	}
	defer file.Close()

	// Get path from FormData (for creating files in subfolders)
	// If missing, use uploaded file name
	filename := c.PostForm("path")
	if filename == "" {
		filename = fileHeader.Filename
	}

	if err := helpers.UploadFile(namespace, filename, file); err != nil {
		log.Printf("❌ Error saving file: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("✅ File '%s' uploaded to namespace '%s'", filename, namespace)

	// The object written is the one pods mount, and a SubPath mount does not
	// follow it, so pods mounting the file need a restart to see it.
	c.JSON(http.StatusOK, withMountHints(namespace, filename, gin.H{"message": "File uploaded successfully"}))
}

func UpdateFileContent(c *gin.Context) {
	namespace := c.Param("namespace")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	var body struct {
		Content string `json:"content"`
	}

	if err := c.ShouldBindJSON(&body); err != nil {
		log.Printf("❌ Error parsing update JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body. Expected 'content' field"})
		return
	}

	err := helpers.UpdateFileContent(namespace, filename, body.Content)
	if err != nil {
		log.Printf("❌ Error updating content of '%s': %v", filename, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("✅ Updated content of '%s' in namespace '%s'", filename, namespace)
	c.JSON(http.StatusOK, withMountHints(namespace, filename, gin.H{"message": "Content updated successfully"}))
}

func DeleteFile(c *gin.Context) {
	namespace := c.Param("namespace")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	// Try deleting as file first
	fileErr := helpers.DeleteFile(namespace, filename)
	if fileErr == nil {
		log.Printf("✅ File '%s' deleted from namespace '%s'", filename, namespace)
		c.JSON(http.StatusOK, gin.H{"message": "File deleted successfully"})
		return
	}

	// If it fails, try deleting as folder
	folderErr := helpers.DeleteFolder(namespace, filename)
	if folderErr == nil {
		log.Printf("✅ Folder '%s' deleted from namespace '%s'", filename, namespace)
		c.JSON(http.StatusOK, gin.H{"message": "Folder deleted successfully"})
		return
	}

	// If both fail, return error
	log.Printf("❌ Error deleting '%s' in namespace '%s': %v", filename, namespace, fileErr)
	c.JSON(http.StatusInternalServerError, gin.H{"error": fileErr.Error()})
}

// POST /files/:namespace/folder
func CreateFolder(c *gin.Context) {
	namespace := c.Param("namespace")

	var body struct {
		Path string `json:"path" binding:"required"`
	}

	if err := c.ShouldBindJSON(&body); err != nil {
		log.Printf("❌ Error parsing JSON for folder creation: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "'path' field is required"})
		return
	}

	if err := helpers.CreateDirectory(namespace, body.Path); err != nil {
		log.Printf("❌ Error creating folder '%s' in '%s': %v", body.Path, namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("✅ Folder '%s' created in namespace '%s'", body.Path, namespace)
	c.JSON(http.StatusOK, gin.H{"message": "Folder created successfully"})
}

// DeleteAllNamespaceFiles removes every file and folder inside the namespace file-manager directory,
// then recreates the empty directory so the namespace stays usable.
// DELETE /files/:namespace
func DeleteAllNamespaceFiles(c *gin.Context) {
	namespace := c.Param("namespace")

	if err := helpers.ValidateNamespaceEnabled(namespace); err != nil {
		log.Printf("❌ Invalid namespace: %v", err)
		switch {
		case strings.Contains(err.Error(), "does not exist"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "is not enabled for KubeNDT"):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	if err := helpers.DeleteNamespaceFilesAll(namespace); err != nil {
		log.Printf("❌ Error deleting all files for namespace '%s': %v", namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not delete namespace files"})
		return
	}

	log.Printf("✅ All files deleted for namespace '%s'", namespace)
	c.JSON(http.StatusOK, gin.H{"message": "All files deleted successfully"})
}

// DELETE /files/:namespace/folder/:folderpath
func DeleteFolder(c *gin.Context) {
	namespace := c.Param("namespace")
	folderPath := c.Param("folderpath")

	if err := helpers.DeleteFolder(namespace, folderPath); err != nil {
		log.Printf("❌ Error deleting folder '%s' in '%s': %v", folderPath, namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("✅ Folder '%s' deleted from namespace '%s'", folderPath, namespace)
	c.JSON(http.StatusOK, gin.H{"message": "Folder deleted successfully"})
}

// POST /files/:namespace/import
func ImportArchive(c *gin.Context) {
	namespace := c.Param("namespace")

	// Get uploaded archive
	fileHeader, err := c.FormFile("file")
	if err != nil {
		log.Printf("❌ Error reading file: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "File not provided"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		log.Printf("❌ Error opening file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not open file"})
		return
	}
	defer file.Close()

	// Detect file type by extension
	filename := fileHeader.Filename
	var importErr error
	var report helpers.ImportReport

	if strings.HasSuffix(filename, ".zip") {
		report, importErr = helpers.ExtractZip(namespace, file)
	} else if strings.HasSuffix(filename, ".tar.gz") || strings.HasSuffix(filename, ".tgz") {
		report, importErr = helpers.ExtractTarGz(namespace, file)
	} else {
		log.Printf("❌ Unsupported file type: %s", filename)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only .zip, .tar.gz and .tgz are supported"})
		return
	}

	if importErr != nil {
		log.Printf("❌ Error importing file '%s': %v", filename, importErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": importErr.Error()})
		return
	}

	log.Printf("✅ File '%s' imported (%d files, %d skipped) into namespace '%s'", filename, report.Imported, len(report.Skipped), namespace)
	c.JSON(http.StatusOK, types.ImportArchiveResponse{Message: "File imported successfully", Imported: report.Imported, Skipped: report.Skipped})
}

// POST /files/:namespace/rename
func RenameFile(c *gin.Context) {
	namespace := c.Param("namespace")

	var body struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}

	if err := c.ShouldBindJSON(&body); err != nil {
		log.Printf("❌ Error parsing rename JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "'oldPath' and 'newPath' are required"})
		return
	}

	if err := helpers.RenameFile(namespace, body.OldPath, body.NewPath); err != nil {
		log.Printf("❌ Error renaming %s to %s: %v", body.OldPath, body.NewPath, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("✅ Renamed '%s' to '%s' in namespace '%s'", body.OldPath, body.NewPath, namespace)
	c.JSON(http.StatusOK, gin.H{"message": "Renamed successfully"})
}

// PUT /file-meta/:namespace/*filename: toggle the sensitive flag on a
// namespace file. Triggers a re-materialisation of the K8s resource that
// backs the file (if any) so it switches between ConfigMap and Secret
// according to the new flag. The pod mounting the file still needs a
// restart to actually see the new resource type because SubPath bind
// mounts are static.
func UpdateFileMeta(c *gin.Context) {
	namespace := c.Param("namespace")
	filename := strings.TrimPrefix(c.Param("filename"), "/")
	filename = strings.TrimSuffix(filename, "/meta")

	var body struct {
		Sensitive *bool `json:"sensitive"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Sensitive == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected JSON body with a boolean 'sensitive' field"})
		return
	}

	if err := helpers.SetFileSensitive(namespace, filename, *body.Sensitive); err != nil {
		log.Printf("❌ could not set sensitive flag on '%s' in '%s': %v", filename, namespace, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// The file is the other kind of object now; pods that mount it keep the
	// one they mounted until they are redeployed, so say so.
	resp := gin.H{
		"message":   "Sensitive flag updated",
		"sensitive": *body.Sensitive,
	}
	if mounted, err := helpers.CountMountedPodsForFile(namespace, filename); err == nil {
		resp["pods_mounting"] = mounted
		if mounted > 0 {
			resp["redeploy_required"] = true
			resp["note"] = "This file is currently mounted by pods. They will continue to use the previous resource type until you redeploy the topology (or clear and re-import)."
		}
	}
	c.JSON(http.StatusOK, resp)
}

// GET /files/:namespace/export
func ExportArchive(c *gin.Context) {
	namespace := c.Param("namespace")
	format := c.DefaultQuery("format", "zip")

	var contentType, ext string
	var stream func(string, io.Writer) error
	switch format {
	case "zip":
		contentType, ext, stream = "application/zip", "zip", helpers.ExportAsZip
	case "tar.gz", "tgz":
		contentType, ext, stream = "application/gzip", "tar.gz", helpers.ExportAsTarGz
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported format. Use 'zip' or 'tar.gz'."})
		return
	}

	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", "attachment; filename=\""+namespace+"."+ext+"\"")
	c.Status(http.StatusOK)

	// Headers are already on the wire; a mid-stream failure truncates the
	// archive and the client sees a corrupt download.
	if err := stream(namespace, c.Writer); err != nil {
		log.Printf("❌ Error exporting namespace '%s': %v", namespace, err)
		return
	}
}

// withMountHints adds, when pods mount the file, what the UI needs to say
// about the restart they need: the object's kind and how many pods.
func withMountHints(namespace, filename string, resp gin.H) gin.H {
	mounted, err := helpers.CountMountedPodsForFile(namespace, filename)
	if err != nil || mounted == 0 {
		return resp
	}
	kind := "ConfigMap"
	if meta, err := helpers.GetFileMeta(namespace, filename); err == nil && meta.Sensitive {
		kind = "Secret"
	}
	resp["resource_synced"] = true
	resp["resource_kind"] = kind
	resp["pods_mounting"] = mounted
	return resp
}
