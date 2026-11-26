import React, { createContext, useCallback, useContext, useEffect, useState } from 'react';

// The light or dark theme of the whole UI. The choice is per browser: light,
// dark, or the system's (nothing stored). index.html sets data-theme on <html>
// before the app draws; this keeps it in step with the choice and with the
// system while the choice is the system's. The CSS needs nothing else, only
// the code that paints with resolved colors reads the theme from here.

const KEY = 'kubendt.theme';
export const THEME_CHOICES = ['system', 'light', 'dark'];

const readChoice = () => {
  try {
    const v = window.localStorage.getItem(KEY);
    return v === 'light' || v === 'dark' ? v : 'system';
  } catch {
    return 'system';
  }
};

const writeChoice = (choice) => {
  try {
    if (choice === 'system') window.localStorage.removeItem(KEY);
    else window.localStorage.setItem(KEY, choice);
  } catch {
    // A browser that keeps nothing still switches for this page.
  }
};

const darkQuery = () =>
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null;

const systemTheme = () => (darkQuery()?.matches ? 'dark' : 'light');

const resolve = (choice) => (choice === 'system' ? systemTheme() : choice);

const ThemeContext = createContext({
  choice: 'system',
  theme: 'light',
  setChoice: () => {},
});

// The attribute changes before the state does, so the code that reads
// resolved colors while rendering already sees the new theme.
const applyTheme = (theme) => {
  document.documentElement.setAttribute('data-theme', theme);
};

export function ThemeProvider({ children }) {
  const [choice, setChoiceState] = useState(readChoice);
  const [theme, setTheme] = useState(() => resolve(readChoice()));

  const show = useCallback((next) => {
    applyTheme(next);
    setTheme(next);
  }, []);

  useEffect(() => {
    applyTheme(theme);
  }, [theme]);

  useEffect(() => {
    if (choice !== 'system') return undefined;
    const q = darkQuery();
    if (!q) return undefined;
    const onChange = () => show(systemTheme());
    q.addEventListener?.('change', onChange);
    return () => q.removeEventListener?.('change', onChange);
  }, [choice, show]);

  const setChoice = useCallback(
    (next) => {
      writeChoice(next);
      show(resolve(next));
      setChoiceState(next);
    },
    [show]
  );

  return (
    <ThemeContext.Provider value={{ choice, theme, setChoice }}>{children}</ThemeContext.Provider>
  );
}

export const useTheme = () => useContext(ThemeContext);
