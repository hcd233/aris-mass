"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { startThemeTransition, type Point } from "@/lib/motion/theme-transition";
import {
  DARK_SCHEME_QUERY,
  THEME_STORAGE_KEY,
  isThemeName,
  parsePreference,
  resolveTheme,
  type ThemeName,
  type ThemePreference,
} from "@/lib/theme-preference";

// THEME_INIT_SCRIPT lives with the pure preference logic; re-exported so the
// root layout keeps importing it from here.
export { THEME_INIT_SCRIPT } from "@/lib/theme-preference";
export type { ThemeName, ThemePreference } from "@/lib/theme-preference";

/* ============================================================
 * Theme registry
 * ------------------------------------------------------------
 * Add a new skin: append a ThemeName, register it in THEMES,
 * add a CSS `[data-theme="<name>"]` block in globals.css, and
 * extend the color palettes below. No other code needs to change.
 * ========================================================== */

const DEFAULT_THEME: ThemeName = "anthropic";

/** Per-theme display metadata consumed by non-CSS consumers (e.g. sonner). */
export interface ThemeMeta {
  /** Maps our skin onto sonner's light/dark/system theming. */
  sonner: "light" | "dark" | "system";
}

export const THEMES: Record<ThemeName, ThemeMeta> = {
  anthropic: { sonner: "light" },
  moonshot: { sonner: "dark" },
};

/* ============================================================
 * Color palettes
 * recharts needs raw color strings, so palettes are plain data
 * keyed by theme — no CSS variables, no runtime resolution.
 * ========================================================== */

const CHART_SERIES_COLORS: Record<ThemeName, readonly string[]> = {
  anthropic: [
    "#D97757",
    "#5B8DB8",
    "#7C6BA5",
    "#4A9E7D",
    "#C76B8A",
    "#8B7355",
    "#6B8BA4",
    "#A0522D",
  ],
  /* Moonlit neutrals: achromatic greys lead; desaturated cool tints
   * (moonlight cream, ice blue) only where series must diverge. */
  moonshot: [
    "#FFFFFF",
    "#BFBFBF",
    "#FFEED9",
    "#8C8C8C",
    "#A8BDD8",
    "#595959",
    "#D9CBB8",
    "#6E7B8C",
  ],
};

export interface StatusColors {
  positive: string;
  warning: string;
  negative: string;
  muted: string;
}

/* Status palette for recharts (mirrors --positive/--warning/--negative in
 * globals.css; recharts needs raw strings). Used by the dashboard reliability
 * bars and health dots. */
const STATUS_COLORS: Record<ThemeName, StatusColors> = {
  anthropic: { positive: "#4F7F6F", warning: "#B9852F", negative: "#B3564F", muted: "#8C7E72" },
  moonshot: { positive: "#A8BDD8", warning: "#FFEED9", negative: "#E07A80", muted: "#595959" },
};

function chartSeriesColors(theme: ThemeName): readonly string[] {
  return CHART_SERIES_COLORS[theme];
}

function readPreference(): ThemePreference {
  try {
    return parsePreference(localStorage.getItem(THEME_STORAGE_KEY));
  } catch {
    return "system";
  }
}

function systemPrefersDark(): boolean {
  return window.matchMedia(DARK_SCHEME_QUERY).matches;
}

/* ============================================================
 * ThemeProvider + useTheme
 * ------------------------------------------------------------
 * Single source of truth for the active theme. Components read
 * via useTheme(); mutations go through setTheme() which updates
 * <html data-theme>, localStorage and context state atomically.
 * Replaces the three separate MutationObservers that previously
 * watched <html data-theme> from theme.ts, theme-switcher and
 * particle-background.
 * ========================================================== */

interface ThemeContextValue {
  /** The skin actually rendered (system preference already resolved). */
  theme: ThemeName;
  preference: ThemePreference;
  /** Pin an explicit skin. `origin` (viewport px) anchors the View Transition. */
  setTheme: (next: ThemeName, origin?: Point) => void;
  setPreference: (next: ThemePreference, origin?: Point) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

const TRANSITION_MS = 400;

/** Writes <html data-theme>, animating only when the rendered skin changes. */
function switchSkin(next: ThemeName, origin: Point | undefined, apply: () => void) {
  const root = document.documentElement;
  const commit = () => {
    root.dataset.theme = next;
    apply();
  };
  if (root.dataset.theme === next) {
    commit();
    return;
  }
  // Signature Moment: eclipse / ink-wash View Transition between worlds.
  if (startThemeTransition(next, origin, commit)) return;
  // Fallback (no View Transitions, or Motion Tier `still`): color fade.
  root.classList.add("theme-transition");
  commit();
  window.setTimeout(() => root.classList.remove("theme-transition"), TRANSITION_MS);
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  // The inline init script guarantees <html data-theme> is set
  // before hydration, so this is safe. We keep DEFAULT_THEME on
  // both server and first client render to stay hydration-safe;
  // the effect syncs the real value right after mount.
  const [theme, setThemeState] = useState<ThemeName>(DEFAULT_THEME);
  const [preference, setPreferenceState] = useState<ThemePreference>("system");

  /* eslint-disable react-hooks/set-state-in-effect -- Reading <html data-theme> (set by inline script) on mount requires setting state in effect */
  useEffect(() => {
    const current = document.documentElement.dataset.theme;
    setThemeState(isThemeName(current) ? current : DEFAULT_THEME);
    setPreferenceState(readPreference());
  }, []);
  /* eslint-enable react-hooks/set-state-in-effect */

  const setPreference = useCallback((next: ThemePreference, origin?: Point) => {
    try {
      if (next === "system") localStorage.removeItem(THEME_STORAGE_KEY);
      else localStorage.setItem(THEME_STORAGE_KEY, next);
    } catch {
      // private mode etc.: theme still applies for this session
    }
    const skin = resolveTheme(next, systemPrefersDark());
    switchSkin(skin, origin, () => {
      setThemeState(skin);
      setPreferenceState(next);
    });
  }, []);

  const setTheme = useCallback(
    (next: ThemeName, origin?: Point) => setPreference(next, origin),
    [setPreference],
  );

  // Following the system: re-resolve whenever the OS appearance flips.
  useEffect(() => {
    if (preference !== "system") return;
    const query = window.matchMedia(DARK_SCHEME_QUERY);
    const onChange = () => {
      const skin = resolveTheme("system", query.matches);
      switchSkin(skin, undefined, () => setThemeState(skin));
    };
    query.addEventListener("change", onChange);
    return () => query.removeEventListener("change", onChange);
  }, [preference]);

  const value = useMemo(
    () => ({ theme, preference, setTheme, setPreference }),
    [theme, preference, setTheme, setPreference],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) {
    throw new Error("useTheme must be used within a ThemeProvider");
  }
  return ctx;
}

/* ============================================================
 * Chart color hooks (backward-compatible with the old API)
 * Components call these and re-render when the theme changes.
 * ========================================================== */

export function useChartSeriesColors(): readonly string[] {
  return chartSeriesColors(useTheme().theme);
}

export function useStatusColors(): StatusColors {
  return STATUS_COLORS[useTheme().theme];
}
