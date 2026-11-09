/* Theme preference: pure logic shared by ThemeProvider (lib/theme.tsx) and the
 * first-paint inline script. No React / DOM imports so it stays testable in
 * node (theme-preference.test.ts). */

export type ThemeName = "anthropic" | "moonshot";

/** An explicit skin, or "system" (the default — nothing stored) which follows
 * prefers-color-scheme: dark → moonshot, light → anthropic. */
export type ThemePreference = ThemeName | "system";

export const THEME_STORAGE_KEY = "theme";
export const DARK_SCHEME_QUERY = "(prefers-color-scheme: dark)";

export function isThemeName(x: string | null | undefined): x is ThemeName {
  return x === "anthropic" || x === "moonshot";
}

/** A stored value that is not an explicit skin means "follow the system". */
export function parsePreference(stored: string | null): ThemePreference {
  return isThemeName(stored) ? stored : "system";
}

export function resolveTheme(preference: ThemePreference, systemDark: boolean): ThemeName {
  if (preference !== "system") return preference;
  return systemDark ? "moonshot" : "anthropic";
}

/* Inline init script (FOUC prevention): rendered as a raw synchronous
 * <script> by the root layout, so the correct [data-theme] is on <html>
 * before first paint. A hand-written ES5 mirror of parsePreference +
 * resolveTheme, kept in lockstep by theme-preference.test.ts. */
export const THEME_INIT_SCRIPT =
  "(function(){var d=document.documentElement;try{var t=localStorage.getItem('theme');" +
  "if(t!=='moonshot'&&t!=='anthropic'){t=window.matchMedia('(prefers-color-scheme: dark)').matches?'moonshot':'anthropic';}" +
  "d.dataset.theme=t;}catch(e){d.dataset.theme='anthropic';}})();";
