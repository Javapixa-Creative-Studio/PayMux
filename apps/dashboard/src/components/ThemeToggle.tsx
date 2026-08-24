import { useState } from 'react';

import { readTheme, setTheme, type Theme } from '../lib/theme';

const OPTIONS: Array<{ value: Theme; label: string; title: string }> = [
  { value: 'system', label: 'Auto', title: 'Follow the operating system' },
  { value: 'light', label: 'Light', title: 'Always light' },
  { value: 'dark', label: 'Dark', title: 'Always dark' },
];

/**
 * Choosing a theme.
 *
 * Auto comes first and is the default, because following the system is the
 * right answer for most people and the one they get by never touching this.
 * The other two exist for the operators who do not want a console changing
 * under them at sunset.
 */
export function ThemeToggle() {
  const [theme, setLocal] = useState<Theme>(() => readTheme());

  const choose = (next: Theme) => {
    setTheme(next);
    setLocal(next);
  };

  return (
    <div className="themepick" role="group" aria-label="Theme">
      {OPTIONS.map((option) => (
        <button
          key={option.value}
          type="button"
          title={option.title}
          aria-pressed={theme === option.value}
          className={theme === option.value ? 'themepick__opt is-on' : 'themepick__opt'}
          onClick={() => choose(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
