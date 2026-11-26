import { render, screen, fireEvent } from '@testing-library/react';
import { ThemeProvider } from './theme';
import ThemeToggle from './components/ThemeToggle';

// jsdom has no matchMedia, so "the system's" resolves to light here.
test('the switch goes system, light, dark and back, and the browser keeps it', () => {
  window.localStorage.removeItem('kubendt.theme');
  render(
    <ThemeProvider>
      <ThemeToggle />
    </ThemeProvider>
  );
  const button = screen.getByRole('button');
  const root = document.documentElement;

  expect(button).toHaveAccessibleName('Theme: follows the system');
  expect(root.getAttribute('data-theme')).toBe('light');

  fireEvent.click(button);
  expect(button).toHaveAccessibleName('Theme: light');
  expect(window.localStorage.getItem('kubendt.theme')).toBe('light');

  fireEvent.click(button);
  expect(button).toHaveAccessibleName('Theme: dark');
  expect(root.getAttribute('data-theme')).toBe('dark');
  expect(window.localStorage.getItem('kubendt.theme')).toBe('dark');

  fireEvent.click(button);
  expect(button).toHaveAccessibleName('Theme: follows the system');
  expect(root.getAttribute('data-theme')).toBe('light');
  expect(window.localStorage.getItem('kubendt.theme')).toBeNull();
});
