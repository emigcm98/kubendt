import React, { useEffect, useRef, useCallback, useState } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import 'xterm/css/xterm.css';
import { ReactComponent as RefreshIcon } from '../assets/images/icons/refresh.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import './PodInteractiveShellModal.css';
import { WS_BASE_URL } from '../config';

const PodInteractiveShellModal = ({
  shellId,
  podName,
  namespace,
  shellMode = 'sh',
  onClose,
  onMinimize,
  minimized = false,
  zIndex = 1000,
  onBringToFront,
}) => {
  const terminalRef = useRef(null);
  const containerRef = useRef(null);
  const headerRef = useRef(null);
  const term = useRef(null);
  const fitAddon = useRef(null);
  const ws = useRef(null);
  const isInitializedRef = useRef(false);
  const isDraggingRef = useRef(false);
  const dragOffsetRef = useRef({ x: 0, y: 0 });
  const positionRef = useRef({ x: 100 + Math.random() * 200, y: 100 + Math.random() * 200 });
  const sizeConfirmedRef = useRef(false);
  const pendingSizeRef = useRef(null); // Save size to send when WS is open

  // Connection lifecycle: "connecting" → "open" → "lost" (terminal idle, awaiting reconnect)
  const [connectionState, setConnectionState] = useState('connecting');

  const cleanupSocket = useCallback(() => {
    if (ws.current) {
      try {
        // Detach handlers first so our onclose doesn't flip state during teardown.
        ws.current.onopen = null;
        ws.current.onmessage = null;
        ws.current.onerror = null;
        ws.current.onclose = null;
        if (
          ws.current.readyState === WebSocket.OPEN ||
          ws.current.readyState === WebSocket.CONNECTING
        ) {
          ws.current.close(1000, 'Client closing');
        }
      } catch (e) {
        console.error(`[${podName}] Error closing WS:`, e);
      }
      ws.current = null;
    }
    sizeConfirmedRef.current = false;
  }, [podName]);

  const handleClose = useCallback(() => {
    cleanupSocket();

    if (term.current) {
      try {
        term.current.dispose();
      } catch (e) {
        console.error(`[${podName}] Error disposing terminal:`, e);
      }
      term.current = null;
    }

    fitAddon.current = null;
    isInitializedRef.current = false;

    onClose();
  }, [podName, onClose, cleanupSocket]);

  const sendCurrentSize = useCallback(() => {
    if (!term.current || !fitAddon.current) return;
    try {
      fitAddon.current.fit();
      const size = {
        type: 'resize',
        size: { cols: term.current.cols, rows: term.current.rows },
      };
      if (ws.current?.readyState === WebSocket.OPEN) {
        ws.current.send(JSON.stringify(size));
        sizeConfirmedRef.current = true;
      } else {
        pendingSizeRef.current = size;
      }
    } catch (e) {
      console.error(`[${podName}] Error in fit:`, e);
    }
  }, [podName]);

  const connect = useCallback(() => {
    if (ws.current) return; // already connecting/connected
    setConnectionState('connecting');
    sizeConfirmedRef.current = false;

    const wsUrl = `${WS_BASE_URL}/shell/ws/${namespace}/${podName}?mode=${encodeURIComponent(shellMode)}`;
    const socket = new WebSocket(wsUrl);
    socket.binaryType = 'arraybuffer';
    ws.current = socket;

    socket.onopen = () => {
      setConnectionState('open');
      // Send the pending size (or a freshly-computed one) so the backend PTY matches.
      if (pendingSizeRef.current) {
        socket.send(JSON.stringify(pendingSizeRef.current));
        pendingSizeRef.current = null;
        setTimeout(() => {
          sizeConfirmedRef.current = true;
        }, 200);
      } else {
        sendCurrentSize();
        setTimeout(() => {
          sizeConfirmedRef.current = true;
        }, 200);
      }
    };

    socket.onmessage = (event) => {
      if (!term.current) return;
      if (event.data instanceof ArrayBuffer) {
        term.current.write(new TextDecoder('utf-8').decode(event.data));
        return;
      }
      if (event.data instanceof Blob) {
        event.data.arrayBuffer().then((buffer) => {
          term.current?.write(new TextDecoder('utf-8').decode(buffer));
        });
      } else {
        term.current.write(event.data);
      }
    };

    socket.onerror = (err) => {
      console.error(`[${podName}] WebSocket error:`, err);
      if (term.current) {
        term.current.write('\r\n\x1b[31m❌ Connection error\x1b[0m\r\n');
      }
    };

    socket.onclose = () => {
      // Drop the reference so a Reconnect click can build a new socket.
      ws.current = null;
      sizeConfirmedRef.current = false;
      setConnectionState('lost');
    };
  }, [namespace, podName, shellMode, sendCurrentSize]);

  const handleReconnect = useCallback(() => {
    if (connectionState === 'open') return;
    cleanupSocket();
    connect();
  }, [connectionState, cleanupSocket, connect]);

  const handleClear = useCallback(() => {
    term.current?.clear();
  }, []);

  // Handle dragging - only on the header
  const handleHeaderMouseDown = (e) => {
    if (onBringToFront) onBringToFront();

    if (e.target.closest('.shell-modal-close-btn')) return;
    if (e.target.closest('.shell-modal-minimize-btn')) return;
    if (e.target.closest('.shell-modal-clear-btn')) return;
    if (e.target.closest('.shell-modal-reconnect-btn')) return;

    isDraggingRef.current = true;
    dragOffsetRef.current = {
      x: e.clientX - positionRef.current.x,
      y: e.clientY - positionRef.current.y,
    };
    e.preventDefault();
  };

  useEffect(() => {
    const handleMouseMove = (e) => {
      if (!isDraggingRef.current || !containerRef.current) return;
      // Clamp to the viewport so the shell can't be dragged off-screen.
      // Losing the header would mean no way to close/minimize/reconnect.
      // The shell uses absolute left/top (not transform), so it clamps
      // against [0, vw-w] and [0, vh-h] directly.
      const w = containerRef.current.offsetWidth;
      const h = containerRef.current.offsetHeight;
      const rawX = e.clientX - dragOffsetRef.current.x;
      const rawY = e.clientY - dragOffsetRef.current.y;
      const maxX = Math.max(0, window.innerWidth - w);
      const maxY = Math.max(0, window.innerHeight - h);
      positionRef.current = {
        x: Math.max(0, Math.min(maxX, rawX)),
        y: Math.max(0, Math.min(maxY, rawY)),
      };
      containerRef.current.style.left = `${positionRef.current.x}px`;
      containerRef.current.style.top = `${positionRef.current.y}px`;
    };

    const handleMouseUp = () => {
      isDraggingRef.current = false;
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);

    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, []);

  // Initialize terminal + WebSocket (only once per mount)
  useEffect(() => {
    if (isInitializedRef.current) return;
    isInitializedRef.current = true;

    term.current = new Terminal({
      cursorBlink: true,
      disableStdin: false,
      fontSize: 15,
      theme: { background: '#1e1e1e', foreground: '#e0e0e0' },
      fontFamily: "'Menlo', 'Monaco', 'Courier New', monospace",
      scrollback: 1000,
    });

    fitAddon.current = new FitAddon();
    term.current.loadAddon(fitAddon.current);

    if (terminalRef.current && term.current) {
      term.current.open(terminalRef.current);
      term.current.focus();

      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          if (!term.current || !fitAddon.current) return;
          try {
            fitAddon.current.fit();
            pendingSizeRef.current = {
              type: 'resize',
              size: { cols: term.current.cols, rows: term.current.rows },
            };
            if (ws.current?.readyState === WebSocket.OPEN) {
              ws.current.send(JSON.stringify(pendingSizeRef.current));
              pendingSizeRef.current = null;
            }
          } catch (e) {
            console.error(`[${podName}] Error in fit:`, e);
          }
        });
      });
    }

    term.current.onData((data) => {
      if (!sizeConfirmedRef.current) return;
      if (ws.current?.readyState === WebSocket.OPEN) {
        ws.current.send(JSON.stringify({ type: 'input', data }));
      }
      // Ctrl+L: keep terminal in sync with shell's clear behaviour.
      if (data === '\x0c') {
        term.current.reset();
      }
    });

    connect();

    const handleResize = () => {
      if (fitAddon.current && term.current && terminalRef.current) {
        try {
          fitAddon.current.fit();
          if (ws.current?.readyState === WebSocket.OPEN) {
            ws.current.send(
              JSON.stringify({
                type: 'resize',
                size: { cols: term.current.cols, rows: term.current.rows },
              })
            );
          }
        } catch (e) {
          console.warn(`[${podName}] Error fitting on resize:`, e);
        }
      }
    };

    window.addEventListener('resize', handleResize);

    return () => {
      window.removeEventListener('resize', handleResize);
      // Closing from the minimized-taskbar chip unmounts the modal directly
      // (without going through handleClose). Make sure the WS is closed
      // gracefully here so the backend logs a normal closure instead of
      // an abnormal exit.
      cleanupSocket();
      if (term.current) {
        try {
          term.current.dispose();
        } catch (e) {
          /* ignore */
        }
        term.current = null;
      }
      fitAddon.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Position container and apply z-index
  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.style.left = `${positionRef.current.x}px`;
      containerRef.current.style.top = `${positionRef.current.y}px`;
      containerRef.current.style.zIndex = zIndex;
    }
  }, [zIndex]);

  // When the shell is restored from minimized state, re-fit so the terminal
  // recalculates its dimensions and the prompt sits correctly.
  useEffect(() => {
    if (minimized) return;
    const t = setTimeout(() => {
      if (!fitAddon.current || !term.current) return;
      try {
        fitAddon.current.fit();
        if (ws.current?.readyState === WebSocket.OPEN) {
          ws.current.send(
            JSON.stringify({
              type: 'resize',
              size: { cols: term.current.cols, rows: term.current.rows },
            })
          );
        }
      } catch (e) {
        console.warn(`[${podName}] Error fitting after restore:`, e);
      }
    }, 60);
    return () => clearTimeout(t);
  }, [minimized, podName]);

  return (
    <div
      ref={containerRef}
      className={`shell-modal-container${minimized ? ' shell-modal-minimized' : ''}`}
      onClick={() => {
        if (!minimized && onBringToFront) onBringToFront();
      }}
    >
      <div ref={headerRef} className="shell-modal-header" onMouseDown={handleHeaderMouseDown}>
        <div className="shell-modal-title">
          <span
            className={`shell-status-dot shell-status-${connectionState}`}
            title={`Status: ${connectionState}`}
          />
          <span className="shell-pod-name">{podName}</span>
          <span className="shell-pod-status">
            {' '}
            -{' '}
            {shellMode === 'serial'
              ? 'Serial Shell'
              : shellMode === 'vtysh'
                ? 'vtysh Console'
                : 'Pod Shell (sh)'}
          </span>
        </div>
        <div className="shell-modal-buttons">
          <button
            className="shell-modal-clear-btn"
            title="Clear terminal output"
            onClick={handleClear}
          >
            Clear
          </button>
          <button
            className="shell-modal-reconnect-btn"
            title={connectionState === 'lost' ? 'Reconnect' : 'Force reconnect'}
            onClick={handleReconnect}
          >
            <RefreshIcon className="app-icon" />
          </button>
          {onMinimize && (
            <button className="shell-modal-minimize-btn" title="Minimize" onClick={onMinimize}>
              −
            </button>
          )}
          <button onClick={handleClose} className="shell-modal-close-btn" title="Close terminal">
            ✖
          </button>
        </div>
      </div>

      <div ref={terminalRef} className="modal-terminal" />

      {connectionState === 'lost' && (
        <div className="shell-disconnected-overlay" role="alertdialog" aria-modal="false">
          <div className="shell-disconnected-card">
            <WarningIcon className="shell-disconnected-icon" />
            <div className="shell-disconnected-title">Connection lost</div>
            <div className="shell-disconnected-subtitle">
              The shell stream to <strong>{podName}</strong> was closed.
            </div>
            <div className="shell-disconnected-actions">
              <button
                className="shell-disconnected-btn shell-disconnected-btn-primary"
                onClick={handleReconnect}
              >
                <RefreshIcon className="app-icon" /> Reconnect
              </button>
              <button className="shell-disconnected-btn" onClick={handleClose}>
                ✖ Close
              </button>
            </div>
          </div>
        </div>
      )}

      {connectionState === 'connecting' && (
        <div className="shell-connecting-indicator">Connecting…</div>
      )}
    </div>
  );
};

export default PodInteractiveShellModal;
