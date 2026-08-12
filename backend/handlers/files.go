// handlers/files.go
package handlers

import (
	"io"
	"kubendt/helpers"
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

	// Refresh the K8s resource (ConfigMap or Secret) that backs this file,
	// if one exists (i.e. some pod already mounts it). Pods need a restart
	// to see the new content because SubPath bind mounts don't propagate
	// updates; surface the count so the UI can prompt the user.
	resp := gin.H{"message": "File uploaded successfully"}
	if synced, kind, err := helpers.SyncMountResourceFromFile(namespace, filename); err != nil {
		log.Printf("⚠️ resource sync after upload of '%s' failed: %v", filename, err)
		resp["sync_warning"] = err.Error()
	} else if synced {
		resp["resource_synced"] = true
		resp["resource_kind"] = kind
		if mounted, err := helpers.CountMountedPodsForFile(namespace, filename); err == nil {
			resp["pods_mounting"] = mounted
		}
	}
	c.JSON(http.StatusOK, resp)
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

	// Same resource sync as in upload. Restart of mounting pods is still
	// required to surface the new content because SubPath bind mounts are
	// static against ConfigMap/Secret updates.
	resp := gin.H{"message": "Content updated successfully"}
	if synced, kind, err := helpers.SyncMountResourceFromFile(namespace, filename); err != nil {
		log.Printf("⚠️ resource sync after edit of '%s' failed: %v", filename, err)
		resp["sync_warning"] = err.Error()
	} else if synced {
		resp["resource_synced"] = true
		resp["resource_kind"] = kind
		if mounted, err := helpers.CountMountedPodsForFile(namespace, filename); err == nil {
			resp["pods_mounting"] = mounted
		}
	}
	c.JSON(http.StatusOK, resp)
}

func DeleteFile(c *gin.Context) {
	namespace := c.Param("namespace")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	// Try deleting as file first
	fileErr := helpers.DeleteFile(namespace, filename)
	if fileErr == nil {
		if err := helpers.DeleteFileMeta(namespace, filename); err != nil {
			log.Printf("⚠️ could not delete file meta for '%s' in '%s': %v", filename, namespace, err)
		}
		log.Printf("✅ File '%s' deleted from namespace '%s'", filename, namespace)
		c.JSON(http.StatusOK, gin.H{"message": "File deleted successfully"})
		return
	}

	// If it fails, try deleting as folder
	folderErr := helpers.DeleteFolder(namespace, filename)
	if folderErr == nil {
		if err := helpers.DeleteFileMetaByPrefix(namespace, filename); err != nil {
			log.Printf("⚠️ could not delete file meta under '%s' in '%s': %v", filename, namespace, err)
		}
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

	// Recreate the empty directory so future uploads work immediately.
	if err := helpers.CreateNamespaceFileDir(namespace); err != nil {
		log.Printf("⚠️ Could not recreate files dir for namespace '%s': %v", namespace, err)
	}

	if err := helpers.DeleteAllFileMetaForNamespace(namespace); err != nil {
		log.Printf("⚠️ could not clear file meta for namespace '%s': %v", namespace, err)
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
	var imported int

	if strings.HasSuffix(filename, ".zip") {
		imported, importErr = helpers.ExtractZip(namespace, file)
	} else if strings.HasSuffix(filename, ".tar.gz") || strings.HasSuffix(filename, ".tgz") {
		imported, importErr = helpers.ExtractTarGz(namespace, file)
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

	log.Printf("✅ File '%s' imported (%d files) into namespace '%s'", filename, imported, namespace)
	c.JSON(http.StatusOK, gin.H{"message": "File imported successfully", "imported": imported})
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

	if err := helpers.RenameFileMeta(namespace, body.OldPath, body.NewPath); err != nil {
		log.Printf("⚠️ could not move file meta from '%s' to '%s' in '%s': %v", body.OldPath, body.NewPath, namespace, err)
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

	// The flag flip does NOT change file content, so nothing to sync into
	// K8s right now: any existing ConfigMap or Secret keeps the same data.
	// What the flag affects is the resource TYPE chosen the next time
	// CreateMountResourceForFile runs (i.e. on the next deploy/modify
	// that materialises this mount). Pods that already mount the file
	// keep using whatever resource was originally created for them; they
	// will switch to the new type only on redeploy. Report this clearly
	// so the UI can prompt the user.
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
