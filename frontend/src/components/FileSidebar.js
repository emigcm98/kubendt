import React, { useEffect, useMemo, useRef, useState } from 'react';
import Tooltip from './Tooltip';
import './FileSidebar.css';

// Walk the tree and collect every folder path that contains at least one node
// (file or folder) whose name OR path matches the search query.
const collectMatchingFolders = (nodes, query, acc = new Set()) => {
  if (!Array.isArray(nodes)) return acc;
  for (const node of nodes) {
    if (node.isDir) {
      if (collectMatchingFolders(node.children || [], query, acc).size > acc.size) {
        // child contained a match → expand this folder too
      }
      const nameMatch = node.name?.toLowerCase().includes(query);
      const pathMatch = node.path?.toLowerCase().includes(query);
      const childMatched = (node.children || []).some((c) => nodeMatchesQuery(c, query));
      if (nameMatch || pathMatch || childMatched) {
        acc.add(node.path);
      }
    }
  }
  return acc;
};

const nodeMatchesQuery = (node, query) => {
  if (!query) return true;
  const q = query.toLowerCase();
  if (node.name?.toLowerCase().includes(q)) return true;
  if (node.path?.toLowerCase().includes(q)) return true;
  if (node.isDir && Array.isArray(node.children)) {
    return node.children.some((c) => nodeMatchesQuery(c, q));
  }
  return false;
};

// Parent folder of a file path. Returns null for root-level files.
const parentFolderOf = (filePath) => {
  if (!filePath) return null;
  const idx = filePath.lastIndexOf('/');
  if (idx <= 0) return null;
  return filePath.slice(0, idx);
};

// localStorage key for persisting the expanded-folders set per namespace.
const expandedFoldersStorageKey = (ns) => `kubendt.expandedFolders.${ns}`;

function FileSidebar({
  namespace,
  files,
  selectedFile,
  unsavedFiles,
  onSelect,
  onRefresh,
  onImport,
  onContextMenu,
  contextMenu,
  onCreateFileInFolder,
  onCreateFolderInFolder,
  onRename,
  onRequestRename,
  onExport,
  onCreateFileRoot,
  onCreateFolderRoot,
  onRequestDeleteFile,
  onToggleSensitive,
  onRequestDeleteFolder,
  onDeleteAllFiles,
}) {
  // Restore from localStorage on mount; namespace change remounts the
  // component (it's in the URL), so the lazy init is enough.
  const [expandedFolders, setExpandedFolders] = useState(() => {
    if (!namespace) return new Set();
    try {
      const raw = localStorage.getItem(expandedFoldersStorageKey(namespace));
      return raw ? new Set(JSON.parse(raw)) : new Set();
    } catch {
      return new Set();
    }
  });
  const [draggedNode, setDraggedNode] = useState(null);
  const [dragOverNode, setDragOverNode] = useState(null);
  const [searchQuery, setSearchQuery] = useState('');

  const dragExpandTimerRef = useRef(null);
  // Ref instead of state for the dragover target, survives React batching
  // and the high-frequency dragover events.
  const lastDragOverPathRef = useRef(null);
  // path → <li> element, populated via ref callback for scrollIntoView.
  const rowRefs = useRef(new Map());

  // Persist expanded folders whenever the set changes.
  useEffect(() => {
    if (!namespace) return;
    try {
      localStorage.setItem(
        expandedFoldersStorageKey(namespace),
        JSON.stringify(Array.from(expandedFolders))
      );
    } catch {
      // Quota exceeded or storage disabled, ignore.
    }
  }, [namespace, expandedFolders]);

  // When the open file changes, expand every ancestor folder so the row
  // is reachable in the tree (mirrors VSCode's "reveal in explorer").
  useEffect(() => {
    if (!selectedFile) return;
    const parts = selectedFile.split('/');
    if (parts.length <= 1) return;
    const ancestors = [];
    for (let i = 1; i < parts.length; i++) {
      ancestors.push(parts.slice(0, i).join('/'));
    }
    setExpandedFolders((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const a of ancestors) {
        if (!next.has(a)) {
          next.add(a);
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [selectedFile]);

  // After the expand-ancestors effect flushes, scroll the selected row
  // into view (no-op if already visible thanks to block: 'nearest').
  useEffect(() => {
    if (!selectedFile) return;
    const t = setTimeout(() => {
      const el = rowRefs.current.get(selectedFile);
      if (el && typeof el.scrollIntoView === 'function') {
        el.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      }
    }, 60);
    return () => clearTimeout(t);
  }, [selectedFile]);

  const normalizedQuery = searchQuery.trim().toLowerCase();

  const searchExpandedFolders = useMemo(() => {
    if (!normalizedQuery) return null;
    return collectMatchingFolders(files || [], normalizedQuery);
  }, [files, normalizedQuery]);

  // Folders are click-to-toggle and never ".selected" (VSCode-style).
  // Creation context = open file's parent folder, or root if nothing open.
  const currentContextFolder = selectedFile ? parentFolderOf(selectedFile) : null;

  const toggleFolder = (folderPath) => {
    const newExpanded = new Set(expandedFolders);
    if (newExpanded.has(folderPath)) {
      newExpanded.delete(folderPath);
    } else {
      newExpanded.add(folderPath);
    }
    setExpandedFolders(newExpanded);
  };

  const expandFolder = (folderPath) => {
    setExpandedFolders((prev) => {
      if (prev.has(folderPath)) return prev;
      const next = new Set(prev);
      next.add(folderPath);
      return next;
    });
  };

  const isFolderExpanded = (folderPath) => {
    if (searchExpandedFolders && searchExpandedFolders.has(folderPath)) return true;
    return expandedFolders.has(folderPath);
  };

  const handleDragStart = (e, node) => {
    e.stopPropagation();
    setDraggedNode(node);
    e.dataTransfer.effectAllowed = 'move';
  };

  const handleDragOver = (e, node) => {
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = 'move';
    if (!node.isDir) return;

    // Visual highlight (drives the .drag-over class). Uses state because
    // it has to trigger a re-render.
    if (dragOverNode !== node.path) {
      setDragOverNode(node.path);
    }

    // Schedule via ref so the dragover firehose only resets the timer
    // when the hovered folder actually changes.
    if (lastDragOverPathRef.current !== node.path) {
      lastDragOverPathRef.current = node.path;
      clearTimeout(dragExpandTimerRef.current);
      // VSCode-style: hover over a collapsed folder for ~500 ms
      // auto-expands it so the user can drop deeper.
      dragExpandTimerRef.current = setTimeout(() => {
        expandFolder(node.path);
      }, 500);
    }
  };

  const handleDragLeave = (e) => {
    e.stopPropagation();
    // Ignore dragleave fired by entering a child (icon, name, expand-btn).
    if (e.currentTarget.contains(e.relatedTarget)) return;
    clearTimeout(dragExpandTimerRef.current);
    lastDragOverPathRef.current = null;
    setDragOverNode(null);
  };

  const handleDrop = (e, targetNode) => {
    e.preventDefault();
    e.stopPropagation();
    clearTimeout(dragExpandTimerRef.current);
    lastDragOverPathRef.current = null;
    setDragOverNode(null);

    if (!draggedNode || !targetNode.isDir) return;
    if (draggedNode.path === targetNode.path) return;

    if (draggedNode.isDir && targetNode.path.startsWith(draggedNode.path + '/')) {
      return;
    }

    // Expand the destination folder so the dropped item is immediately
    // visible after the rename completes.
    expandFolder(targetNode.path);

    const destPath = targetNode.path + '/' + draggedNode.name;
    onRename?.(draggedNode.path, destPath);
    setDraggedNode(null);
  };

  // Single onClick on the <li> so the row's full hit area lands (padding
  // included). Folders toggle, files forward selection upstream.
  const handleRowClick = (e, node) => {
    e.stopPropagation();
    if (node.isDir) {
      toggleFolder(node.path);
    } else {
      onSelect?.(node.path);
    }
  };

  // Click on empty area inside the file-list → close the open file. The
  // visual highlight disappears because selectedFile is now empty.
  const handleEmptyAreaClick = (e) => {
    if (e.target.closest('li')) return;
    onSelect?.('');
  };

  // Header create buttons resolve the target folder from the active level
  // and pre-expand it so the new item is visible the moment files refresh.
  const handleCreateFileClick = () => {
    if (currentContextFolder) {
      expandFolder(currentContextFolder);
      onCreateFileInFolder?.(currentContextFolder);
    } else {
      onCreateFileRoot?.();
    }
  };

  const handleCreateFolderClick = () => {
    if (currentContextFolder) {
      expandFolder(currentContextFolder);
      onCreateFolderInFolder?.(currentContextFolder);
    } else {
      onCreateFolderRoot?.();
    }
  };

  const sortNodes = (nodes) => {
    if (!nodes) return [];
    return [...nodes].sort((a, b) => {
      if (a.isDir && !b.isDir) return -1;
      if (!a.isDir && b.isDir) return 1;
      return a.name.localeCompare(b.name);
    });
  };

  const renderHighlightedName = (name) => {
    if (!normalizedQuery) return name;
    const lower = name.toLowerCase();
    const idx = lower.indexOf(normalizedQuery);
    if (idx < 0) return name;
    return (
      <>
        {name.slice(0, idx)}
        <mark className="file-search-hit">{name.slice(idx, idx + normalizedQuery.length)}</mark>
        {name.slice(idx + normalizedQuery.length)}
      </>
    );
  };

  const renderTreeNode = (node, depth = 0) => {
    if (normalizedQuery && !nodeMatchesQuery(node, normalizedQuery)) return null;

    // Only files can be highlighted; folders are pure expand/collapse rows.
    const isHighlighted = !node.isDir && node.path === selectedFile;
    const isExpanded = isFolderExpanded(node.path);
    const isDraggedOver = dragOverNode === node.path;

    return (
      <div key={node.path}>
        <li
          ref={(el) => {
            // Track every rendered row by path so scrollIntoView can find
            // the selected file without DOM queries.
            if (el) rowRefs.current.set(node.path, el);
            else rowRefs.current.delete(node.path);
          }}
          className={`${isHighlighted ? 'selected' : ''} ${node.isDir ? 'folder' : 'file'} ${isDraggedOver ? 'drag-over' : ''}`}
          style={{ marginLeft: `${depth * 16}px` }}
          onClick={(e) => handleRowClick(e, node)}
          onContextMenu={(e) => {
            e.preventDefault();
            if (node.isDir) {
              onContextMenu(e, { type: 'folder', path: node.path });
            } else {
              onContextMenu(e, { type: 'file', path: node.path });
            }
          }}
          draggable
          onDragStart={(e) => handleDragStart(e, node)}
          onDragOver={(e) => handleDragOver(e, node)}
          onDragLeave={handleDragLeave}
          onDrop={(e) => handleDrop(e, node)}
        >
          {node.isDir ? (
            <>
              <button
                className="expand-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  toggleFolder(node.path);
                }}
                aria-label={isExpanded ? 'Collapse' : 'Expand'}
              >
                {isExpanded ? '▼' : '▶'}
              </button>
              <span className="folder-icon">📁</span>
              <span className="folder-name">{renderHighlightedName(node.name)}</span>
            </>
          ) : (
            <>
              <span className="file-icon">📄</span>
              <span className="file-name">
                {renderHighlightedName(node.name)}
                {node.sensitive && (
                  <span
                    className="sensitive-lock"
                    title="Sensitive file (stored as a Kubernetes Secret when mounted)"
                    aria-label="Sensitive"
                  >
                    🔒
                  </span>
                )}
                {unsavedFiles?.has?.(node.path) && (
                  <span className="dirty-dot" aria-label="Unsaved changes">
                    •
                  </span>
                )}
              </span>
            </>
          )}
        </li>
        {node.isDir && isExpanded && node.children && node.children.length > 0 && (
          <ul className="nested-list">
            {sortNodes(node.children).map((child) => renderTreeNode(child, depth + 1))}
          </ul>
        )}
      </div>
    );
  };

  const visibleRoots = normalizedQuery
    ? sortNodes(files || []).filter((node) => nodeMatchesQuery(node, normalizedQuery))
    : sortNodes(files || []);

  // The visible folder highlight in the tree already communicates "where
  // it will land", so the tooltip text stays short and stable.
  const createFileTooltip = 'New file';
  const createFolderTooltip = 'New folder';

  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <span className="sidebar-title">Files</span>
        <div className="actions">
          <Tooltip text="Import archive">
            <button aria-label="Import: upload a ZIP or tar.gz archive" onClick={onImport}>
              📦
            </button>
          </Tooltip>
          <Tooltip text="Export ZIP">
            <button aria-label="Export: download all files as a ZIP archive" onClick={onExport}>
              💾
            </button>
          </Tooltip>
          {onDeleteAllFiles && (
            <Tooltip text="Delete all">
              <button
                className="action-btn-danger"
                aria-label="Delete all files: permanently removes every file and folder"
                onClick={onDeleteAllFiles}
              >
                🗑️
              </button>
            </Tooltip>
          )}
        </div>
      </div>

      <div className="actions actions-quick">
        <Tooltip text={createFileTooltip}>
          <button aria-label={createFileTooltip} onClick={handleCreateFileClick}>
            📄
          </button>
        </Tooltip>
        <Tooltip text={createFolderTooltip}>
          <button aria-label={createFolderTooltip} onClick={handleCreateFolderClick}>
            📁
          </button>
        </Tooltip>
        <Tooltip text="Refresh">
          <button aria-label="Refresh file list" onClick={onRefresh}>
            🔃
          </button>
        </Tooltip>
      </div>

      <div className="sidebar-search">
        <input
          type="text"
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          placeholder="🔍 Filter files…"
          aria-label="Filter files"
        />
        {searchQuery && (
          <Tooltip text="Clear filter">
            <button
              type="button"
              className="sidebar-search-clear"
              aria-label="Clear filter"
              onClick={() => setSearchQuery('')}
            >
              ✖
            </button>
          </Tooltip>
        )}
      </div>

      <div className="sidebar-divider" aria-hidden="true"></div>

      <ul
        className="file-list"
        onContextMenu={(e) => {
          e.preventDefault();
          onContextMenu(e, { type: 'root', path: null });
        }}
        onClick={handleEmptyAreaClick}
      >
        {visibleRoots.map((node) => renderTreeNode(node))}
        {normalizedQuery && visibleRoots.length === 0 && (
          <li className="file-search-empty">No files match “{searchQuery}”</li>
        )}
      </ul>

      {contextMenu && (
        <div
          className="context-menu"
          style={{ top: `${contextMenu.y}px`, left: `${contextMenu.x}px` }}
        >
          {contextMenu.type === 'root' && (
            <>
              <button className="context-menu-item" onClick={() => onCreateFileRoot?.()}>
                📄 New File
              </button>
              <button className="context-menu-item" onClick={() => onCreateFolderRoot?.()}>
                📁 New Folder
              </button>
            </>
          )}

          {contextMenu.type === 'folder' && (
            <>
              <button
                className="context-menu-item"
                onClick={() => onCreateFileInFolder?.(contextMenu.path)}
              >
                📄 New File
              </button>
              <button
                className="context-menu-item"
                onClick={() => onCreateFolderInFolder?.(contextMenu.path)}
              >
                📁 New Folder
              </button>
              <div className="context-menu-divider"></div>
              <button
                className="context-menu-item"
                onClick={() => onRequestRename?.(contextMenu.path)}
              >
                ✏️ Rename
              </button>
              <button
                className="context-menu-item delete"
                onClick={() => onRequestDeleteFolder?.(contextMenu.path)}
              >
                🗑️ Delete
              </button>
            </>
          )}

          {contextMenu.type === 'file' &&
            (() => {
              // Look up the current sensitive flag from the tree so the menu
              // shows the right label and toggles the right way.
              const findNode = (nodes) => {
                for (const n of nodes || []) {
                  if (!n.isDir && n.path === contextMenu.path) return n;
                  if (n.isDir && n.children) {
                    const found = findNode(n.children);
                    if (found) return found;
                  }
                }
                return null;
              };
              const node = findNode(files);
              const isSensitive = !!node?.sensitive;
              return (
                <>
                  <button
                    className="context-menu-item"
                    onClick={() => onRequestRename?.(contextMenu.path)}
                  >
                    ✏️ Rename
                  </button>
                  {onToggleSensitive && (
                    <button
                      className="context-menu-item"
                      onClick={() => onToggleSensitive(contextMenu.path, isSensitive)}
                      title={
                        isSensitive
                          ? 'Stop treating this file as sensitive. Future mounts will use a ConfigMap.'
                          : 'Mark this file as sensitive. Future mounts will use a Secret instead of a ConfigMap.'
                      }
                    >
                      {isSensitive ? '🔓 Mark as non-sensitive' : '🔒 Mark as sensitive'}
                    </button>
                  )}
                  <button
                    className="context-menu-item delete"
                    onClick={() => onRequestDeleteFile?.(contextMenu.path)}
                  >
                    🗑️ Delete
                  </button>
                </>
              );
            })()}
        </div>
      )}
    </div>
  );
}

export default FileSidebar;
