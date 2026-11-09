import { flushSync } from "react-dom";
import { revealRadius, type Point } from "@/lib/motion/theme-transition";

/* Locale switch: a View Transition from the old language to the new one.
 * Layout is Locale-Stable (same type scale, reserved widths, reserved
 * scrollbar gutters), so the snapshots line up and only the glyphs change.
 * With an `origin` (the clicked control) the new language spreads out from
 * it behind a soft edge — the same gesture as the theme switch, but with no
 * color change; without one the page cross-fades in place. Styles live under
 * html[data-locale-vt] in styles/motion-signature.css, kept apart from the
 * theme transition's html[data-vt] so neither cleanup clobbers the other. */

const LOCALE_VT_VARS = ["--lvt-x", "--lvt-y", "--lvt-r"];
let active: ViewTransition | null = null;

/**
 * Runs `apply` inside a View Transition. Returns false (without calling
 * `apply`) when the browser lacks View Transitions or the Motion Tier is
 * `still` — the caller then switches instantly.
 */
export function startLocaleTransition(apply: () => void, origin?: Point): boolean {
  const root = document.documentElement;
  if (typeof document.startViewTransition !== "function" || root.dataset.motionTier === "still") {
    return false;
  }

  // Interruptible: a second switch finishes the running transition at once.
  active?.skipTransition();

  if (origin) {
    const w = window.innerWidth;
    const h = window.innerHeight;
    root.style.setProperty("--lvt-x", `${origin.x}px`);
    root.style.setProperty("--lvt-y", `${origin.y}px`);
    root.style.setProperty("--lvt-r", `${revealRadius(origin.x, origin.y, w, h)}px`);
    root.dataset.localeVt = "reveal";
  } else {
    root.dataset.localeVt = "fade";
  }

  const reset = () => {
    active = null;
    delete root.dataset.localeVt;
    for (const name of LOCALE_VT_VARS) root.style.removeProperty(name);
  };

  try {
    const t = document.startViewTransition(() => flushSync(apply));
    active = t;
    t.ready.catch(() => {});
    t.finished.finally(() => {
      // A skipped transition settles after its successor started.
      if (active === t) reset();
    });
    return true;
  } catch {
    reset();
    return false;
  }
}
