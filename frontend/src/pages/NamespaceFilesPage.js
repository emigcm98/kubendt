import React, { useEffect, useState, useCallback } from 'react';
import { useParams, useNavigate, useLocation } from 'react-router-dom';
import NamespaceFilesNavbar from '../components/NamespaceFilesNavbar';
import FileSidebar from '../components/FileSidebar';
import AlertModal from '../components/AlertModal';
import './NamespaceFilesPage.css';
import ErrorPage from './ErrorPage';
import { API_BASE_URL } from '../config';
import { ReactComponent as BoxIcon } from '../assets/images/icons/box.svg';
import { ReactComponent as SaveIcon } from '../assets/images/icons/save.svg';
import { ReactComponent as LockIcon } from '../assets/images/icons/lock.svg';
import { ReactComponent as EyeIcon } from '../assets/images/icons/eye.svg';
import { ReactComponent as FileIcon } from '../assets/images/icons/file.svg';
import { ReactComponent as FolderIcon } from '../assets/images/icons/folder.svg';
import { ReactComponent as EditIcon } from '../assets/images/icons/edit.svg';

import CodeMirror from '@uiw/react-codemirror';
import { json } from '@codemirror/lang-json';
import { yaml } from '@codemirror/lang-yaml';

function NamespaceFilesPage() {
  const { namespace } = useParams();
  const navigate = useNavigate();
  const location = useLocation();
  const selectedFileFromQuery = new URLSearchParams(location.search).get('file');
  const [files, setFiles] = useState([]);
  const [selectedFile, setSelectedFile] = useState('');

  // Keep the URL's ?file=... in sync with the open file so refreshes,
  // bookmarks and shared links land on the same file. Uses replace: true
  // to avoid pushing a history entry on every click, otherwise the
  // browser back button would step through every file you opened.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const current = params.get('file') || '';
    if (current === selectedFile) return;
    if (selectedFile) {
      params.set('file', selectedFile);
    } else {
      params.delete('file');
    }
    const search = params.toString();
    navigate(`${window.location.pathname}${search ? '?' + search : ''}`, { replace: true });
  }, [selectedFile, navigate]);
  const [fileContent, setFileContent] = useState('');
  const [unsavedFiles, setUnsavedFiles] = useState(new Set());
  const [fetchError, setFetchError] = useState(null);
  const [editedByFile, setEditedByFile] = useState({});
  const [saveStatus, setSaveStatus] = useState(null);
  const [showCreateFileModal, setShowCreateFileModal] = useState(false);
  const [showCreateFolderModal, setShowCreateFolderModal] = useState(false);
  const [showImportModal, setShowImportModal] = useState(false);
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [showDeleteAllModal, setShowDeleteAllModal] = useState(false);
  const [showRenameModal, setShowRenameModal] = useState(false);
  const [showExportModal, setShowExportModal] = useState(false);
  const [exportFormat, setExportFormat] = useState('zip');
  const [showOverwriteModal, setShowOverwriteModal] = useState(false);
  const [pendingArchives, setPendingArchives] = useState([]);
  // True while OS files are being dragged over the editor/sidebar drop zones.
  const [draggingFiles, setDraggingFiles] = useState(false);
  const [newFileName, setNewFileName] = useState('');
  const [newFolderName, setNewFolderName] = useState('');
  const [renameSourcePath, setRenameSourcePath] = useState('');
  const [renameValue, setRenameValue] = useState('');
  const [deleteTarget, setDeleteTarget] = useState(null); // { type: 'file'|'folder', path }
  const [importStatus, setImportStatus] = useState(null);
  const [importMessage, setImportMessage] = useState('');
  const [contextMenu, setContextMenu] = useState(null); // { x, y, folderPath }
  const [toast, setToast] = useState(null); // { message, type: "success" | "error" | "info" }
  // When a sensitive file is opened, hide its content behind a "Show"
  // button until the user explicitly reveals it. Resets on file switch.
  const [revealSensitive, setRevealSensitive] = useState(false);
  const importFileInputRef = React.useRef(null);

  // For creating a file/folder inside a specific folder
  const [targetFolder, setTargetFolder] = useState('');

  const fetchFiles = async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/files/${namespace}`);
      const text = await res.text();

      if (!res.ok) {
        throw new Error(`Error ${res.status}: ${text}`);
      }

      const data = JSON.parse(text);
      const nextFiles = data.files || [];
      setFiles(nextFiles);
      setFetchError(null);
      if (selectedFileFromQuery && fileExists(nextFiles, selectedFileFromQuery)) {
        setSelectedFile(selectedFileFromQuery);
      }
    } catch (err) {
      console.error('❌ Error fetching files:', err);
      setFetchError(err.message);
    }
  };

  const fileExists = (tree, path) => {
    for (const node of tree) {
      if (node.path === path) return true;
      if (node.isDir && node.children) {
        if (fileExists(node.children, path)) return true;
      }
    }
    return false;
  };

  const findFileNode = (tree, path) => {
    for (const node of tree || []) {
      if (!node.isDir && node.path === path) return node;
      if (node.isDir && node.children) {
        const found = findFileNode(node.children, path);
        if (found) return found;
      }
    }
    return null;
  };

  const selectedFileNode = selectedFile ? findFileNode(files, selectedFile) : null;
  const selectedIsSensitive = !!selectedFileNode?.sensitive;

  const fetchFileContent = async (filename) => {
    try {
      const res = await fetch(`${API_BASE_URL}/files/${namespace}/${filename}`);
      const data = await res.text();
      setFileContent(data);
      setEditedByFile((prev) => ({ ...prev, [filename]: data }));
    } catch (err) {
      console.error('❌ Error reading file:', err);
    }
  };

  useEffect(() => {
    fetchFiles();
    // eslint-disable-next-line
  }, []);

  useEffect(() => {
    if (!selectedFile) return;

    if (editedByFile[selectedFile] !== undefined) {
      setFileContent(editedByFile[selectedFile]);
      return;
    }

    if (!unsavedFiles.has(selectedFile)) {
      fetchFileContent(selectedFile);
    }
    // eslint-disable-next-line
  }, [selectedFile, unsavedFiles, files, editedByFile]);

  // Always start with the sensitive content masked when switching files;
  // the user has to opt in to reveal it. Re-masks on file switch.
  useEffect(() => {
    setRevealSensitive(false);
  }, [selectedFile]);

  const handleDeleteFile = async (filename) => {
    try {
      const res = await fetch(`${API_BASE_URL}/files/${namespace}/${filename}`, {
        method: 'DELETE',
      });

      if (res.ok) {
        showToast('File deleted', 'success');
        fetchFiles();
        if (filename === selectedFile) {
          setSelectedFile('');
          setFileContent('');
        }
      } else {
        showToast('Could not delete file', 'error');
      }
    } catch (err) {
      console.error('❌ Error deleting file:', err);
    }
  };

  const handleDeleteFolder = async (folderPath) => {
    try {
      const encodedFolderPath = folderPath
        .split('/')
        .map((part) => encodeURIComponent(part))
        .join('/');

      const res = await fetch(`${API_BASE_URL}/files/${namespace}/${encodedFolderPath}`, {
        method: 'DELETE',
      });

      if (res.ok) {
        showToast('Folder deleted', 'success');
        fetchFiles();
        if (selectedFile.startsWith(folderPath + '/')) {
          setSelectedFile('');
          setFileContent('');
        }
      } else {
        showToast('Could not delete folder', 'error');
      }
    } catch (err) {
      console.error('❌ Error deleting folder:', err);
    }
  };

  const handleDeleteAllFiles = async () => {
    try {
      const res = await fetch(`${API_BASE_URL}/files/${namespace}`, {
        method: 'DELETE',
      });
      if (res.ok) {
        showToast('All files deleted', 'success');
        setSelectedFile('');
        setFileContent('');
        setUnsavedFiles(new Set());
        setEditedByFile({});
        fetchFiles();
      } else {
        const payload = await res.json().catch(() => null);
        showToast(`${payload?.error || 'Could not delete files'}`, 'error');
      }
    } catch (err) {
      console.error('❌ Error deleting all files:', err);
      showToast('Error deleting files', 'error');
    }
  };

  const saveFile = useCallback(
    async ({ filename, content, isNew }) => {
      let url,
        method,
        headers = {},
        body;

      if (isNew) {
        const formData = new FormData();
        formData.append('file', new Blob([content], { type: 'text/plain' }), 'file');
        formData.append('path', filename); // Send full path as field
        url = `${API_BASE_URL}/files/${namespace}/`;
        method = 'POST';
        body = formData;
      } else {
        url = `${API_BASE_URL}/files/${namespace}/${filename}`;
        method = 'PUT';
        headers['Content-Type'] = 'application/json';
        body = JSON.stringify({ content });
      }

      const res = await fetch(url, { method, headers, body });
      const payload = await res.json().catch(() => null);
      if (!res.ok) {
        const message = payload?.error || `HTTP ${res.status}`;
        throw new Error(message);
      }
      return payload || {};
    },
    [namespace]
  );

  const handleSave = useCallback(
    async (opts = {}) => {
      const filename = opts.filename ?? selectedFile;
      const content = opts.content ?? editedByFile[filename] ?? fileContent;
      const isNew = opts.isNew ?? unsavedFiles.has(filename);
      const silent = opts.silent ?? false;
      if (!filename) return;

      if (!isNew && !unsavedFiles.has(filename)) {
        return;
      }

      try {
        setSaveStatus('saving');
        const payload = await saveFile({ filename, content, isNew });

        if (isNew) {
          setUnsavedFiles((prev) => {
            const u = new Set(prev);
            u.delete(filename);
            return u;
          });
          await fetchFiles();
        } else {
          setUnsavedFiles((prev) => {
            const u = new Set(prev);
            u.delete(filename);
            return u;
          });
        }

        setEditedByFile((prev) => ({ ...prev, [filename]: content }));
        setFileContent(content);
        setSaveStatus(null);

        if (!silent) {
          // If the backend resynced a ConfigMap, surface how many pods
          // need a restart for the change to actually appear inside
          // them (SubPath bind mounts don't propagate ConfigMap
          // updates: Kubernetes limitation).
          const pods = payload?.pods_mounting;
          if (payload?.resource_synced && typeof pods === 'number' && pods > 0) {
            const kind = payload?.resource_kind || 'resource';
            showToast(
              `File saved. ${kind} updated; restart ${pods} pod${pods === 1 ? '' : 's'} mounting this file for changes to take effect.`,
              'success',
              6000
            );
          } else if (payload?.sync_warning) {
            showToast(
              `File saved, but resource sync failed: ${payload.sync_warning}`,
              'warning',
              6000
            );
          } else {
            showToast('File saved', 'success');
          }
        }
      } catch (e) {
        const msg = String(e.message || e);
        setSaveStatus(null);
        showToast(`Error saving: ${msg}`, 'error');
        throw e;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [editedByFile, fileContent, saveFile, selectedFile, unsavedFiles]
  );

  useEffect(() => {
    const handleKeyDown = (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        if (selectedFile) {
          handleSave();
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [selectedFile, handleSave]);

  // Warn before leaving (reload / close tab) while edits are unsaved.
  useEffect(() => {
    if (unsavedFiles.size === 0) return;
    const onBeforeUnload = (e) => {
      e.preventDefault();
      e.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [unsavedFiles]);

  // Default toast duration is 3s. Callers can override it for long
  // informational messages (e.g. "N pods need to be restarted") so the
  // user has time to actually read them.
  const showToast = (message, type = 'info', durationMs = 3000) => {
    setToast({ message, type });
    setTimeout(() => setToast(null), durationMs);
  };

  const handleCreateFolder = async () => {
    const folderName = newFolderName.trim();
    if (!folderName) {
      showToast('Folder name cannot be empty', 'error');
      return;
    }
    if (/[\\/]/.test(folderName) || folderName === '.' || folderName === '..') {
      showToast('Invalid folder name', 'error');
      return;
    }

    try {
      let folderPath = folderName;
      if (targetFolder) {
        folderPath = targetFolder + '/' + folderName;
      }

      const res = await fetch(`${API_BASE_URL}/file-ops/${namespace}/folder`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: folderPath }),
      });

      if (res.ok) {
        showToast('Folder created', 'success');
        setShowCreateFolderModal(false);
        setNewFolderName('');
        setTargetFolder('');
        await fetchFiles();
      } else {
        const text = await res.text();
        showToast(`Error: ${text}`, 'error');
      }
    } catch (err) {
      console.error('❌ Error creating folder:', err);
      showToast(`Error: ${err.message}`, 'error');
    }
  };

  const handleConfirmCreateFile = async () => {
    const filename = newFileName.trim();
    if (!filename) {
      showToast('Filename cannot be empty', 'error');
      return;
    }
    if (/[\\/]/.test(filename) || filename === '.' || filename === '..') {
      showToast('Invalid filename', 'error');
      return;
    }

    try {
      let fullPath = filename;
      if (targetFolder) {
        fullPath = targetFolder + '/' + filename;
      }

      await handleSave({ filename: fullPath, content: '', isNew: true, silent: true });
      setSelectedFile(fullPath);
      setFileContent('');
      setEditedByFile((prev) => ({ ...prev, [fullPath]: '' }));
      setShowCreateFileModal(false);
      setNewFileName('');
      setTargetFolder('');
      await fetchFiles();
    } catch {
      /* error is already handled inside */
    }
  };

  const handleImportArchive = async (file) => {
    if (!file) return;

    const validExtensions = ['.zip', '.tar.gz', '.tgz'];
    const filename = file.name.toLowerCase();
    const isValid = validExtensions.some((ext) => filename.endsWith(ext));

    if (!isValid) {
      showToast('Only .zip, .tar.gz and .tgz files are supported', 'error');
      return;
    }

    setImportStatus('importing');
    setImportMessage(`Importing ${filename}...`);

    try {
      const formData = new FormData();
      formData.append('file', file);

      const res = await fetch(`${API_BASE_URL}/file-ops/${namespace}/import`, {
        method: 'POST',
        body: formData,
      });

      const payload = await res.json().catch(() => null);
      if (!res.ok) {
        throw new Error(payload?.error || `HTTP ${res.status}`);
      }

      // An empty archive extracts nothing: say so instead of a false success.
      const imported = payload?.imported;
      if (imported === 0) {
        setImportStatus(null);
        setShowImportModal(false);
        showToast(`${filename} has no files, nothing was imported`, 'warning', 4000);
        return;
      }

      setImportStatus('success');
      setImportMessage(
        typeof imported === 'number'
          ? `${filename} imported (${imported} file${imported === 1 ? '' : 's'})`
          : `${filename} imported successfully`
      );
      setShowImportModal(false);

      setTimeout(() => {
        fetchFiles();
        setImportStatus(null);
      }, 1500);
    } catch (err) {
      console.error('❌ Error importing archive:', err);
      setImportStatus(null);
      showToast(`Error: ${err.message}`, 'error');
    }
  };

  // Import can overwrite files with the same path, so when the namespace
  // already has files, ask before extracting. Empty namespaces import straight.
  const beginImport = (archiveFiles) => {
    const list = Array.from(archiveFiles || []);
    if (list.length === 0) return;
    if ((files || []).length > 0) {
      setPendingArchives(list);
      setShowImportModal(false);
      setShowOverwriteModal(true);
    } else {
      runImports(list);
    }
  };

  const runImports = async (list) => {
    setShowOverwriteModal(false);
    setPendingArchives([]);
    for (const f of list) {
      // eslint-disable-next-line no-await-in-loop
      await handleImportArchive(f);
    }
  };

  const handleContextMenu = (e, context) => {
    e.preventDefault();
    e.stopPropagation();
    setContextMenu({ x: e.clientX, y: e.clientY, ...context });
  };

  const closeContextMenu = () => {
    setContextMenu(null);
  };

  const requestDeleteFile = (path) => {
    setDeleteTarget({ type: 'file', path });
    setShowDeleteModal(true);
    closeContextMenu();
  };

  const requestDeleteFolder = (path) => {
    setDeleteTarget({ type: 'folder', path });
    setShowDeleteModal(true);
    closeContextMenu();
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;

    if (deleteTarget.type === 'folder') {
      await handleDeleteFolder(deleteTarget.path);
    } else {
      await handleDeleteFile(deleteTarget.path);
    }

    setShowDeleteModal(false);
    setDeleteTarget(null);
  };

  const requestRename = (path) => {
    const parts = path.split('/');
    const currentName = parts[parts.length - 1] || path;
    setRenameSourcePath(path);
    setRenameValue(currentName);
    setShowRenameModal(true);
    closeContextMenu();
  };

  const confirmRename = async () => {
    const trimmed = renameValue.trim();
    if (!trimmed) {
      showToast('Name cannot be empty', 'error');
      return;
    }
    if (/[\\/]/.test(trimmed) || trimmed === '.' || trimmed === '..') {
      showToast('Invalid name', 'error');
      return;
    }

    const lastSlash = renameSourcePath.lastIndexOf('/');
    const parentPath = lastSlash >= 0 ? renameSourcePath.slice(0, lastSlash) : '';
    const newPath = parentPath ? `${parentPath}/${trimmed}` : trimmed;

    if (newPath === renameSourcePath) {
      setShowRenameModal(false);
      return;
    }

    await handleRename(renameSourcePath, newPath);
    setShowRenameModal(false);
  };

  const toggleSensitive = useCallback(
    async (path, currentlySensitive) => {
      closeContextMenu();
      try {
        const res = await fetch(`${API_BASE_URL}/file-meta/${namespace}/${path}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ sensitive: !currentlySensitive }),
        });
        const payload = await res.json().catch(() => null);
        if (!res.ok) {
          showToast(payload?.error || `HTTP ${res.status}`, 'error');
          return;
        }
        const nowSensitive = payload?.sensitive ?? !currentlySensitive;
        if (payload?.redeploy_required && payload?.pods_mounting > 0) {
          showToast(
            `Marked ${nowSensitive ? 'sensitive' : 'non-sensitive'}. ${payload.pods_mounting} pod${payload.pods_mounting === 1 ? '' : 's'} currently mount this file with the previous resource type. Redeploy the topology for the new type to take effect.`,
            'warning',
            8000
          );
        } else {
          showToast(`File marked as ${nowSensitive ? 'sensitive' : 'non-sensitive'}`, 'success');
        }
        await fetchFiles();
      } catch (err) {
        console.error('❌ Error toggling sensitive flag:', err);
        showToast(`Error: ${err.message}`, 'error');
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [namespace]
  );

  const handleRename = async (oldPath, newPath) => {
    try {
      const res = await fetch(`${API_BASE_URL}/file-ops/${namespace}/rename`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ oldPath, newPath }),
      });

      if (res.ok) {
        showToast('Renamed successfully', 'success');
        closeContextMenu();
        // If renaming selected file, update selection
        if (selectedFile === oldPath) {
          setSelectedFile(newPath);
          setEditedByFile((prev) => {
            const updated = { ...prev };
            updated[newPath] = updated[oldPath];
            delete updated[oldPath];
            return updated;
          });
          setUnsavedFiles((prev) => {
            const updated = new Set(prev);
            if (updated.has(oldPath)) {
              updated.delete(oldPath);
              updated.add(newPath);
            }
            return updated;
          });
        }
        await fetchFiles();
      } else {
        const text = await res.text();
        showToast(`Error: ${text}`, 'error');
      }
    } catch (err) {
      console.error('❌ Error renaming:', err);
      showToast(`Error: ${err.message}`, 'error');
    }
  };

  const confirmExport = async () => {
    const ext = exportFormat === 'tar.gz' ? 'tar.gz' : 'zip';
    try {
      const res = await fetch(
        `${API_BASE_URL}/file-ops/${namespace}/export?format=${encodeURIComponent(exportFormat)}`
      );
      if (!res.ok) {
        showToast('Could not export files', 'error');
        return;
      }

      const blob = await res.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${namespace}.${ext}`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);

      setShowExportModal(false);
      showToast('Files exported', 'success');
    } catch (err) {
      console.error('❌ Error exporting:', err);
      showToast(`Error: ${err.message}`, 'error');
    }
  };

  // Download a single file to the user's machine.
  const downloadFile = async (path) => {
    closeContextMenu();
    try {
      const res = await fetch(`${API_BASE_URL}/files/${namespace}/${path}`);
      if (!res.ok) {
        showToast('Could not download file', 'error');
        return;
      }
      const blob = await res.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = path.split('/').pop() || 'file';
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
    } catch (err) {
      console.error('❌ Error downloading file:', err);
      showToast(`Error: ${err.message}`, 'error');
    }
  };

  const ARCHIVE_RE = /\.(zip|tar\.gz|tgz)$/i;
  const MAX_FILE_BYTES = 1024 * 1024; // backend caps uploads at 1 MiB

  // Pull the dropped files out of a DataTransfer synchronously (items are only
  // valid during the event) and flag whether a folder was among them, since
  // folders can't be uploaded directly.
  const readDropped = (dataTransfer) => {
    const files = [];
    let hadDirectory = false;
    const items = dataTransfer?.items;
    if (items && items.length > 0) {
      for (const it of items) {
        if (it.kind !== 'file') continue;
        const entry = it.webkitGetAsEntry?.();
        if (entry && entry.isDirectory) {
          hadDirectory = true;
          continue;
        }
        const f = it.getAsFile();
        if (f) files.push(f);
      }
    } else {
      for (const f of dataTransfer?.files || []) files.push(f);
    }
    return { files, hadDirectory };
  };

  // Handle OS files dropped onto the sidebar or the empty editor. Archives go
  // through the import extractor; plain files are uploaded as text into the
  // target folder (root when none).
  const uploadDroppedFiles = async (dataTransfer, targetFolder = '') => {
    const { files: dropped, hadDirectory } = readDropped(dataTransfer);
    setDraggingFiles(false);

    if (hadDirectory) {
      showToast('Folders cannot be dropped here. Zip them and use Import.', 'warning', 5000);
    }
    if (dropped.length === 0) return;

    const tooBig = dropped.filter((f) => f.size > MAX_FILE_BYTES);
    const usable = dropped.filter((f) => f.size <= MAX_FILE_BYTES);
    if (tooBig.length > 0) {
      showToast(
        `Skipped ${tooBig.length} file${tooBig.length === 1 ? '' : 's'} over 1 MiB`,
        'warning',
        5000
      );
    }

    const archives = usable.filter((f) => ARCHIVE_RE.test(f.name));
    const plain = usable.filter((f) => !ARCHIVE_RE.test(f.name));

    let added = 0;
    for (const f of plain) {
      try {
        const text = await f.text();
        const path = targetFolder ? `${targetFolder}/${f.name}` : f.name;
        await saveFile({ filename: path, content: text, isNew: true });
        added += 1;
      } catch (e) {
        showToast(`Could not add ${f.name}: ${e.message || e}`, 'error');
      }
    }

    if (plain.length > 0) {
      await fetchFiles();
      if (added > 0) {
        showToast(`Added ${added} file${added === 1 ? '' : 's'}`, 'success');
      }
    }

    // Archives go through the import flow (which confirms before overwriting).
    if (archives.length > 0) {
      beginImport(archives);
    }
  };

  // True while the drag carries OS files (not an internal tree move).
  const dragHasFiles = (e) => Array.from(e.dataTransfer?.types || []).includes('Files');

  useEffect(() => {
    document.addEventListener('click', closeContextMenu);
    return () => document.removeEventListener('click', closeContextMenu);
  }, []);

  // Close any open modal / context menu with Esc.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'Escape') return;
      if (contextMenu) {
        setContextMenu(null);
        return;
      }
      if (showImportModal) {
        setShowImportModal(false);
        return;
      }
      if (showRenameModal) {
        setShowRenameModal(false);
        return;
      }
      if (showDeleteModal) {
        setShowDeleteModal(false);
        return;
      }
      if (showDeleteAllModal) {
        setShowDeleteAllModal(false);
        return;
      }
      if (showCreateFileModal) {
        setShowCreateFileModal(false);
        return;
      }
      if (showCreateFolderModal) {
        setShowCreateFolderModal(false);
        return;
      }
      if (showExportModal) {
        setShowExportModal(false);
        return;
      }
      if (showOverwriteModal) {
        setShowOverwriteModal(false);
        setPendingArchives([]);
        return;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [
    contextMenu,
    showImportModal,
    showRenameModal,
    showDeleteModal,
    showDeleteAllModal,
    showCreateFileModal,
    showCreateFolderModal,
    showExportModal,
    showOverwriteModal,
  ]);

  const extractStatus = (msg) => {
    const match = msg.match(/Error (\d+):/);
    return match ? parseInt(match[1], 10) : 500;
  };

  if (fetchError) {
    return <ErrorPage statusCode={extractStatus(fetchError)} rawMessage={fetchError} />;
  }

  return (
    <div className="files-page-root">
      <NamespaceFilesNavbar namespace={namespace} onBack={() => navigate(`/${namespace}`)} />
      <div className="files-page">
        <FileSidebar
          namespace={namespace}
          files={files}
          selectedFile={selectedFile}
          unsavedFiles={unsavedFiles}
          onSelect={setSelectedFile}
          onDelete={handleDeleteFile}
          onDeleteFolder={handleDeleteFolder}
          onRefresh={fetchFiles}
          onImport={() => setShowImportModal(true)}
          onContextMenu={handleContextMenu}
          contextMenu={contextMenu}
          onCreateFileInFolder={(folder) => {
            setTargetFolder(folder);
            setShowCreateFileModal(true);
            closeContextMenu();
          }}
          onCreateFolderInFolder={(folder) => {
            setTargetFolder(folder);
            setShowCreateFolderModal(true);
            closeContextMenu();
          }}
          onRename={handleRename}
          onRequestRename={requestRename}
          onRequestDeleteFile={requestDeleteFile}
          onRequestDeleteFolder={requestDeleteFolder}
          onToggleSensitive={toggleSensitive}
          onDownloadFile={downloadFile}
          onExport={() => setShowExportModal(true)}
          onDropFiles={uploadDroppedFiles}
          hasFiles={(files || []).length > 0}
          onCreateFileRoot={() => {
            setTargetFolder('');
            setShowCreateFileModal(true);
            closeContextMenu();
          }}
          onCreateFolderRoot={() => {
            setTargetFolder('');
            setShowCreateFolderModal(true);
            closeContextMenu();
          }}
          onDeleteAllFiles={() => setShowDeleteAllModal(true)}
        />
        <div
          className={`file-editor${draggingFiles && !selectedFile ? ' file-editor-dropping' : ''}`}
          onDragOver={(e) => {
            if (!selectedFile && dragHasFiles(e)) {
              e.preventDefault();
              setDraggingFiles(true);
            }
          }}
          onDragLeave={(e) => {
            if (!selectedFile && !e.currentTarget.contains(e.relatedTarget)) {
              setDraggingFiles(false);
            }
          }}
          onDrop={(e) => {
            if (!selectedFile && dragHasFiles(e)) {
              e.preventDefault();
              uploadDroppedFiles(e.dataTransfer, '');
            }
          }}
        >
          {saveStatus && (
            <div className={`save-indicator save-${saveStatus}`}>
              {saveStatus === 'saving' ? (
                <>
                  <SaveIcon className="app-icon" /> Saving...
                </>
              ) : (
                '✓ Saved'
              )}
            </div>
          )}
          {draggingFiles && !selectedFile && (
            <div className="editor-drop-overlay">
              <BoxIcon className="app-icon" />
              <span>Drop files to add them</span>
            </div>
          )}
          {selectedFile ? (
            <>
              <div className="editor-header">
                <strong>
                  {selectedIsSensitive && (
                    <span
                      className="sensitive-lock"
                      title="Sensitive file (stored as a Kubernetes Secret when mounted)"
                      aria-label="Sensitive"
                    >
                      <LockIcon className="app-icon" />
                    </span>
                  )}
                  {selectedFile}
                  {unsavedFiles.has(selectedFile) && (
                    <span className="dirty-dot-header" title="Unsaved changes">
                      •
                    </span>
                  )}
                </strong>
                <button onClick={handleSave}>
                  <SaveIcon className="app-icon" /> Save File
                </button>
              </div>
              {selectedIsSensitive && !revealSensitive ? (
                <div className="sensitive-shield">
                  <LockIcon className="sensitive-shield-icon" />
                  <div className="sensitive-shield-title">Sensitive content hidden</div>
                  <div className="sensitive-shield-message">
                    This file is marked as sensitive. Its content is materialised as a Kubernetes
                    Secret when mounted.
                  </div>
                  <button
                    type="button"
                    className="sensitive-shield-show"
                    onClick={() => setRevealSensitive(true)}
                  >
                    <EyeIcon className="app-icon" /> Show content
                  </button>
                </div>
              ) : (
                <div className="editor-container">
                  <CodeMirror
                    value={fileContent}
                    extensions={
                      selectedFile?.endsWith('.json')
                        ? [json()]
                        : selectedFile?.endsWith('.yaml') || selectedFile?.endsWith('.yml')
                          ? [yaml()]
                          : []
                    }
                    onChange={(value) => {
                      setFileContent(value);
                      if (selectedFile) {
                        setEditedByFile((prev) => ({ ...prev, [selectedFile]: value }));
                        if (!unsavedFiles.has(selectedFile)) {
                          setUnsavedFiles((prev) => {
                            const u = new Set(prev);
                            u.add(selectedFile);
                            return u;
                          });
                        }
                      }
                    }}
                    editable={true}
                  />
                </div>
              )}
            </>
          ) : (files || []).length === 0 ? (
            <div
              className="editor-empty"
              onContextMenu={(e) => handleContextMenu(e, { type: 'root', path: null })}
            >
              <FileIcon className="editor-empty-icon" />
              <div className="editor-empty-title">No files yet</div>
              <div className="editor-empty-message">
                Create a file or folder, or drop files here to get started.
              </div>
              <div className="editor-empty-actions">
                <button
                  className="btn-create"
                  title="Create a new file"
                  onClick={() => {
                    setTargetFolder('');
                    setShowCreateFileModal(true);
                  }}
                >
                  <FileIcon className="app-icon" /> New file
                </button>
                <button
                  className="btn-neutral"
                  title="Create a new folder"
                  onClick={() => {
                    setTargetFolder('');
                    setShowCreateFolderModal(true);
                  }}
                >
                  <FolderIcon className="app-icon icon-folder" /> New folder
                </button>
                <button
                  className="btn-neutral"
                  title="Import a .zip or .tar.gz archive"
                  onClick={() => setShowImportModal(true)}
                >
                  <BoxIcon className="app-icon" /> Import archive
                </button>
              </div>
            </div>
          ) : (
            <div
              className="editor-placeholder"
              onContextMenu={(e) => handleContextMenu(e, { type: 'root', path: null })}
            >
              <FileIcon className="app-icon" /> Select or create a file to start editing
            </div>
          )}
        </div>
      </div>

      {/* Modal to create folder */}
      {showCreateFolderModal && (
        <div className="create-file-modal-overlay" onClick={() => setShowCreateFolderModal(false)}>
          <div className="create-file-modal" onClick={(e) => e.stopPropagation()}>
            <div className="create-file-modal-header">
              <FolderIcon className="app-icon icon-folder" /> New Folder
            </div>
            <div className="create-file-modal-body">
              {targetFolder && (
                <div className="folder-location">
                  Location: <strong>{targetFolder}</strong>
                </div>
              )}
              <label htmlFor="foldername-input">Folder name</label>
              <input
                id="foldername-input"
                type="text"
                placeholder="my-folder"
                value={newFolderName}
                onChange={(e) => setNewFolderName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    handleCreateFolder();
                  }
                }}
                autoFocus
              />
            </div>
            <div className="create-file-modal-footer">
              <button className="btn-cancel" onClick={() => setShowCreateFolderModal(false)}>
                Cancel
              </button>
              <button className="btn-create" onClick={handleCreateFolder}>
                Create Folder
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modal to create file */}
      {showCreateFileModal && (
        <div className="create-file-modal-overlay" onClick={() => setShowCreateFileModal(false)}>
          <div className="create-file-modal" onClick={(e) => e.stopPropagation()}>
            <div className="create-file-modal-header">
              <FileIcon className="app-icon" /> New File
            </div>
            <div className="create-file-modal-body">
              {targetFolder && (
                <div className="folder-location">
                  Location: <strong>{targetFolder}</strong>
                </div>
              )}
              <label htmlFor="filename-input">Filename</label>
              <input
                id="filename-input"
                type="text"
                placeholder="example.json"
                value={newFileName}
                onChange={(e) => setNewFileName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    handleConfirmCreateFile();
                  }
                }}
                autoFocus
              />
            </div>
            <div className="create-file-modal-footer">
              <button className="btn-cancel" onClick={() => setShowCreateFileModal(false)}>
                Cancel
              </button>
              <button className="btn-create" onClick={handleConfirmCreateFile}>
                Create File
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modal to confirm delete */}
      <AlertModal
        isOpen={showDeleteModal && !!deleteTarget}
        type="warning"
        danger
        title={deleteTarget?.type === 'folder' ? 'Delete folder' : 'Delete file'}
        message={
          deleteTarget?.type === 'folder' ? (
            <>
              Delete folder <strong>'{deleteTarget?.path}'</strong> and all its contents?
            </>
          ) : (
            <>
              Delete file <strong>'{deleteTarget?.path}'</strong>?
            </>
          )
        }
        confirmText="Delete"
        cancelText="Cancel"
        onConfirm={confirmDelete}
        onCancel={() => setShowDeleteModal(false)}
      />

      <AlertModal
        isOpen={showDeleteAllModal}
        type="warning"
        danger
        title="Delete all files"
        message={
          <>
            Delete <strong>all files and folders</strong> in namespace{' '}
            <strong>'{namespace}'</strong>?
          </>
        }
        confirmText="Delete all"
        cancelText="Cancel"
        onConfirm={() => {
          setShowDeleteAllModal(false);
          handleDeleteAllFiles();
        }}
        onCancel={() => setShowDeleteAllModal(false)}
      />

      {/* Modal to rename file/folder */}
      {showRenameModal && (
        <div className="create-file-modal-overlay" onClick={() => setShowRenameModal(false)}>
          <div className="create-file-modal" onClick={(e) => e.stopPropagation()}>
            <div className="create-file-modal-header">
              <EditIcon className="app-icon" /> Rename
            </div>
            <div className="create-file-modal-body">
              <div className="folder-location">
                Current: <strong>{renameSourcePath}</strong>
              </div>
              <label htmlFor="rename-input">New name</label>
              <input
                id="rename-input"
                type="text"
                value={renameValue}
                onChange={(e) => setRenameValue(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    confirmRename();
                  }
                }}
                autoFocus
              />
            </div>
            <div className="create-file-modal-footer">
              <button className="btn-cancel" onClick={() => setShowRenameModal(false)}>
                Cancel
              </button>
              <button className="btn-create" onClick={confirmRename}>
                Rename
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modal to import archive */}
      {showImportModal && (
        <div className="import-modal-overlay" onClick={() => setShowImportModal(false)}>
          <div className="import-modal" onClick={(e) => e.stopPropagation()}>
            <div className="import-modal-header">
              <BoxIcon className="app-icon icon-import" /> Import Archive
            </div>
            <div className="import-modal-body">
              <input
                ref={importFileInputRef}
                type="file"
                accept=".zip,.tar.gz,.tgz"
                style={{ display: 'none' }}
                onChange={(e) => {
                  if (e.target.files && e.target.files.length > 0) {
                    beginImport([e.target.files[0]]);
                  }
                  e.target.value = '';
                }}
              />
              <div
                role="button"
                tabIndex={0}
                className="import-drop-zone"
                onClick={() => importFileInputRef.current?.click()}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    importFileInputRef.current?.click();
                  }
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  const files = e.dataTransfer.files;
                  if (files && files.length > 0) {
                    beginImport([files[0]]);
                  }
                }}
                onDragOver={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                }}
              >
                <div className="drop-zone-content">
                  <FolderIcon
                    style={{
                      width: '2.5rem',
                      height: '2.5rem',
                      marginBottom: '0.5rem',
                      color: '#d99a2b',
                    }}
                  />
                  <div style={{ fontWeight: 'bold', marginBottom: '0.3rem' }}>
                    Drop archive here
                  </div>
                  <div style={{ fontSize: '0.85rem', color: '#666' }}>or click to select</div>
                </div>
              </div>
              <div style={{ marginTop: '1rem', fontSize: '0.85rem', color: '#666' }}>
                Supported formats: .zip, .tar.gz, .tgz
              </div>
            </div>
            <div className="import-modal-footer">
              <button className="btn-cancel" onClick={() => setShowImportModal(false)}>
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modal to export as an archive */}
      {showExportModal && (
        <div className="create-file-modal-overlay" onClick={() => setShowExportModal(false)}>
          <div className="create-file-modal" onClick={(e) => e.stopPropagation()}>
            <div className="create-file-modal-header">
              <SaveIcon className="app-icon" /> Export files
            </div>
            <div className="create-file-modal-body">
              <label htmlFor="export-format">Format</label>
              <select
                id="export-format"
                className="export-format-select"
                title="Choose the archive format"
                value={exportFormat}
                onChange={(e) => setExportFormat(e.target.value)}
                autoFocus
              >
                <option value="zip">ZIP (.zip)</option>
                <option value="tar.gz">Gzipped tar (.tar.gz)</option>
              </select>
              {unsavedFiles.size > 0 && (
                <p className="export-unsaved-note">
                  {unsavedFiles.size} file{unsavedFiles.size === 1 ? ' has' : 's have'} unsaved
                  changes. The export uses the last saved version, save first to include them.
                </p>
              )}
            </div>
            <div className="create-file-modal-footer">
              <button className="btn-cancel" onClick={() => setShowExportModal(false)}>
                Cancel
              </button>
              <button className="btn-create" onClick={confirmExport}>
                <SaveIcon className="app-icon" /> Export
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modal: confirm import into a non-empty namespace */}
      <AlertModal
        isOpen={showOverwriteModal}
        type="confirm"
        title="Import into a non-empty namespace"
        message={`Importing ${
          pendingArchives.length === 1 ? 'this archive' : `these ${pendingArchives.length} archives`
        } may overwrite files that share the same path. Files with different names are kept.`}
        confirmText="Import anyway"
        cancelText="Cancel"
        onConfirm={() => runImports(pendingArchives)}
        onCancel={() => {
          setShowOverwriteModal(false);
          setPendingArchives([]);
        }}
      />

      {/* Import status indicator */}
      {importStatus && (
        <div className={`import-status import-status-${importStatus}`}>{importMessage}</div>
      )}

      {/* Toast notifications */}
      {toast && <div className={`toast toast-${toast.type}`}>{toast.message}</div>}
    </div>
  );
}

export default NamespaceFilesPage;
