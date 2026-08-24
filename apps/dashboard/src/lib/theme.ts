/**
 * The viewer's theme preference.
 *
 * Three states, not two. "System" is the default and follows the operating
 * system, which is what most people want and what they get by never opening
 * this setting. Light and dark are explicit overrides that have to win against
 * the system in both directions.
 *
 * The preference is per-browser and never leaves it: it is a comfort setting
 * about one person's screen, not something an operator configures for a
 * deployment.
 */
export type Theme = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'paymux.theme';

export function isTheme(value: unknown): value is Theme {
  return value === 'system' || value === 'light' || value === 'dark';
}

/** Reads the stored preference, defaulting to following the system. */
export function readTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    return isTheme(stored) ? stored : 'system';
  } catch {
    // Private windows and blocked site data both throw here. Following the
    // system is a perfectly good answer, so this is not worth reporting.
    return 'system';
  }
}

/**
 * Applies a theme by stamping the root element.
 *
 * "System" removes the attribute rather than resolving it to light or dark,
 * so the CSS media query stays in charge and the page follows along when the
 * operating system changes while it is open.
 */
export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  if (theme === 'system') {
    root.removeAttribute('data-theme');
  } else {
    root.setAttribute('data-theme', theme);
  }
}

/** Stores and applies a choice. */
export function setTheme(theme: Theme): void {
  applyTheme(theme);
  try {
    if (theme === 'system') {
      localStorage.removeItem(STORAGE_KEY);
    } else {
      localStorage.setItem(STORAGE_KEY, theme);
    }
  } catch {
    // The choice still applies for this page; it just will not be remembered.
  }
}
