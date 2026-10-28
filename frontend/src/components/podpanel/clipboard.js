// Copy text to the clipboard with a graceful fallback. navigator.clipboard
// is only available in secure contexts (HTTPS or localhost), so production
// deployments served over plain HTTP must fall back to the legacy
// execCommand('copy') trick via a hidden textarea.
export const copyToClipboard = async (text) => {
  if (text == null) return false;
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // fall through to the legacy path
    }
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'absolute';
    ta.style.left = '-9999px';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
};
