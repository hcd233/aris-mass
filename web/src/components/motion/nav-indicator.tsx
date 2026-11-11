"use client";

import { useEffect, useLayoutEffect, useRef } from "react";
import { useMotionTier } from "@/lib/motion/tier-store";
import { springAtRest, springStep, type SpringParams, type SpringState } from "@/lib/motion/spring";

/* Each edge rides its own spring: the edge facing the travel direction leads,
 * the other trails, so the indicator stretches toward the destination and
 * contracts as it lands — one continuous object instead of a jump. */
interface EdgeSprings {
  lead: SpringParams;
  trail: SpringParams;
}
/** Atelier: both edges critically damped — the marker is dragged, never bounces. */
const ATELIER_EDGES: EdgeSprings = {
  lead: { stiffness: 700, damping: 53, mass: 1 },
  trail: { stiffness: 420, damping: 41, mass: 1 },
};
/** Lunar: the leading edge overshoots slightly (ζ≈0.81, ≤1.5%) and the body
 * stays taut, so the pill reads as elastic instead of wobbling as a whole. */
const LUNAR_EDGES: EdgeSprings = {
  lead: { stiffness: 520, damping: 37, mass: 1 },
  trail: { stiffness: 340, damping: 36, mass: 1 },
};
/** Lunar afterglow: a softer spring trailing the pill like a comet tail. */
const GLOW_SPRING: SpringParams = { stiffness: 140, damping: 20, mass: 1 };
const SWIPE_MS = 320;
const MAX_DT_S = 0.05;
const REST_PX = 0.05;

/**
 * Top of `el` relative to `ancestor`'s padding box — the containing block of the
 * absolutely positioned indicator. Read from layout offsets on purpose: the rows
 * rise from `translateY(8px)` (atelier-rise, `backwards` fill) and
 * `getBoundingClientRect()` reports that first frame, while the indicator measures
 * once per `layoutKey` — the skewed read stuck 8px below the row for good.
 */
function layoutTopWithin(el: HTMLElement, ancestor: HTMLElement): number {
  let top = 0;
  let node: HTMLElement | null = el;
  while (node && node !== ancestor) {
    top += node.offsetTop;
    node = node.offsetParent as HTMLElement | null;
  }
  return top;
}

interface IndicatorDriver {
  moveTo: (row: HTMLElement, animate: boolean) => void;
  hide: () => void;
  dispose: () => void;
}

function createDriver(el: HTMLElement, glowEl: HTMLElement, nav: HTMLElement): IndicatorDriver {
  let placed = false;
  let everPlaced = false;
  let top: SpringState = { x: 0, v: 0 };
  let bottom: SpringState = { x: 0, v: 0 };
  let glow: SpringState = { x: 0, v: 0 };
  let target = { top: 0, bottom: 0 };
  let leadIsBottom = true;
  let lunar = false;
  let raf = 0;
  let last = 0;

  const write = () => {
    el.style.transform = `translateY(${top.x.toFixed(2)}px)`;
    el.style.height = `${Math.max(bottom.x - top.x, 0).toFixed(2)}px`;
    glowEl.style.transform = `translateY(${glow.x.toFixed(2)}px)`;
    glowEl.style.height = `${target.bottom - target.top}px`;
  };

  const step = (now: number) => {
    const dt = Math.min((now - last) / 1000, MAX_DT_S);
    last = now;
    const edges = lunar ? LUNAR_EDGES : ATELIER_EDGES;
    top = springStep(top, target.top, dt, leadIsBottom ? edges.trail : edges.lead);
    bottom = springStep(bottom, target.bottom, dt, leadIsBottom ? edges.lead : edges.trail);
    glow = lunar ? springStep(glow, target.top, dt, GLOW_SPRING) : top;
    write();
    const rest =
      springAtRest(top, target.top, REST_PX) &&
      springAtRest(bottom, target.bottom, REST_PX) &&
      springAtRest(glow, target.top, REST_PX);
    raf = rest ? 0 : requestAnimationFrame(step);
  };

  return {
    moveTo(row, animate) {
      const rowTop = layoutTopWithin(row, nav);
      const next = { top: rowTop, bottom: rowTop + row.offsetHeight };
      const appearing = !placed;
      delete el.dataset.hidden;
      delete glowEl.dataset.hidden;
      lunar = document.documentElement.dataset.theme === "moonshot";

      // Snap: first paint, reduced motion, or reappearing (travel from a stale
      // spot would cross rows that were never involved).
      if (!animate || appearing) {
        cancelAnimationFrame(raf);
        raf = 0;
        target = next;
        top = { x: next.top, v: 0 };
        bottom = { x: next.bottom, v: 0 };
        glow = { x: next.top, v: 0 };
        write();
        // 荧光笔: the stroke is swiped in when the highlight comes back.
        if (animate && everPlaced && !lunar) {
          el.animate(
            [{ clipPath: "inset(0 100% 0 0 round 8px)" }, { clipPath: "inset(0 0 0 0 round 8px)" }],
            { duration: SWIPE_MS, easing: "cubic-bezier(0.16, 1, 0.3, 1)" },
          );
        }
        placed = true;
        everPlaced = true;
        return;
      }

      if (next.top === target.top && next.bottom === target.bottom) return;
      // Compare centers so a re-target mid-stretch still picks the right lead.
      leadIsBottom = next.top + next.bottom > top.x + bottom.x;
      target = next;
      if (raf === 0) {
        last = performance.now();
        raf = requestAnimationFrame(step);
      }
    },
    hide() {
      el.dataset.hidden = "";
      glowEl.dataset.hidden = "";
      placed = false;
    },
    dispose() {
      cancelAnimationFrame(raf);
      raf = 0;
    },
  };
}

/**
 * Sidebar navigation indicator (spec §7.4): one element that travels to the
 * active item (`[data-nav-item][data-active]` inside the parent <nav>) — a
 * marker stroke dragged across in atelier, an elastic glowing pill with a
 * trailing afterglow in lunar. It starts moving on click (before the route
 * resolves) and re-targets whenever `layoutKey` changes (route, open groups,
 * collapsed sidebar).
 */
export function NavIndicator({ layoutKey }: { layoutKey: string }) {
  const tier = useMotionTier();
  const ref = useRef<HTMLSpanElement>(null);
  const glowRef = useRef<HTMLSpanElement>(null);
  const driver = useRef<IndicatorDriver | null>(null);
  const tierRef = useRef(tier);

  useLayoutEffect(() => {
    tierRef.current = tier;
  }, [tier]);

  useLayoutEffect(() => {
    const el = ref.current;
    const glowEl = glowRef.current;
    const nav = el?.parentElement;
    if (!el || !glowEl || !nav) return;
    const d = createDriver(el, glowEl, nav);
    driver.current = d;
    return () => {
      d.dispose();
      driver.current = null;
    };
  }, []);

  // Layout effect: the first placement must land before paint so the active
  // item never flashes without its highlight.
  useLayoutEffect(() => {
    const d = driver.current;
    const nav = ref.current?.parentElement;
    if (!d || !nav) return;
    const active = nav.querySelector<HTMLElement>("[data-nav-item][data-active]");
    if (active) d.moveTo(active, tier !== "still");
    else d.hide();
  }, [layoutKey, tier]);

  // Respond to the click itself: the route (and data-active) only flips once
  // the next page resolves, which made the highlight lag behind the pointer.
  useEffect(() => {
    const nav = ref.current?.parentElement;
    if (!nav) return;
    const onClick = (e: MouseEvent) => {
      if (e.defaultPrevented || e.button !== 0) return;
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const row = (e.target as Element | null)?.closest<HTMLElement>("a[data-nav-item]");
      if (!row || !nav.contains(row) || row.getAttribute("aria-disabled") === "true") return;
      driver.current?.moveTo(row, tierRef.current !== "still");
    };
    nav.addEventListener("click", onClick);
    return () => nav.removeEventListener("click", onClick);
  }, []);

  return (
    <>
      <span ref={glowRef} aria-hidden="true" className="nav-indicator-trail" data-hidden="" />
      <span ref={ref} aria-hidden="true" className="nav-indicator" data-hidden="" />
    </>
  );
}
