import { useCallback, useEffect, useRef, useState } from 'react';

// Delays the real close until the slide-out animation finishes, so the info
// panels animate off-screen instead of vanishing. The consumer applies the
// `is-closing` class while closing and calls handleAnimationEnd on the panel.
//
// closeSignal lets a parent request an animated close (e.g. clicking the graph
// background) without unmounting the panel outright: bump the value and the
// panel plays its exit before onClose runs.
export default function usePanelClose(onClose, closeSignal) {
  const [isClosing, setIsClosing] = useState(false);
  const requestClose = useCallback(() => setIsClosing(true), []);
  const handleAnimationEnd = useCallback(() => {
    if (isClosing) onClose();
  }, [isClosing, onClose]);

  // Skip the initial run so mounting the panel does not close it immediately.
  const firstSignal = useRef(true);
  useEffect(() => {
    if (firstSignal.current) {
      firstSignal.current = false;
      return;
    }
    setIsClosing(true);
  }, [closeSignal]);

  return { isClosing, requestClose, handleAnimationEnd };
}
