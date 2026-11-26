import React from 'react';
import { useTheme } from '../theme';
import { ReactComponent as MonitorIcon } from '../assets/images/icons/monitor.svg';
import { ReactComponent as SunIcon } from '../assets/images/icons/sun.svg';
import { ReactComponent as MoonIcon } from '../assets/images/icons/moon.svg';
import './ThemeToggle.css';

const NEXT = { system: 'light', light: 'dark', dark: 'system' };
const ICONS = { system: MonitorIcon, light: SunIcon, dark: MoonIcon };
const SAID = { system: 'follows the system', light: 'light', dark: 'dark' };

// The theme switch of the bars: one click moves on to the next of system,
// light and dark. `className` is the bar's own button style.
export default function ThemeToggle({ className = '' }) {
  const { choice, setChoice } = useTheme();
  const Icon = ICONS[choice];
  const next = NEXT[choice];
  return (
    <button
      type="button"
      className={`${className} theme-toggle`}
      onClick={() => setChoice(next)}
      title={`Theme: ${SAID[choice]}. Click for ${SAID[next]}`}
      aria-label={`Theme: ${SAID[choice]}`}
    >
      <Icon className="app-icon" aria-hidden="true" />
    </button>
  );
}
