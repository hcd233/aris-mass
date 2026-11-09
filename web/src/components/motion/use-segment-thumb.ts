"use client";

import { useLayoutEffect, useRef } from "react";

/**
 * Sliding selection for a segmented ToggleGroup: instead of the pressed item's
 * own background snapping, the group's ::before (plus a lunar ::after
 * afterglow, styles/motion-*.css) sits under the pressed item and travels to
 * the next one. It watches the items' `data-pressed`, so callers need no
 * wiring — ToggleGroup attaches the returned ref when variant="segmented".
 *
 * Geometry comes from offset* (layout boxes), not getBoundingClientRect: items
 * may still be mid-entrance (`translateY` with `backwards` fill), and a rect
 * read would bake that offset in (NavIndicator hit exactly this, 8px low).
 *
 * data-thumb: `instant` while landing in place (first placement, a resize, a
 * locale switch reflowing the labels), `on` once settled — only a change of
 * the pressed item slides — and `off` while nothing is pressed.
 */
export function useSegmentThumb<T extends HTMLElement>(enabled: boolean) {
  const ref = useRef<T>(null);

  useLayoutEffect(() => {
    const group = ref.current;
    if (!enabled || !group) return;
    let raf = 0;

    const place = (slide: boolean) => {
      const item = group.querySelector<HTMLElement>(":scope > [data-pressed]");
      if (!item) {
        group.dataset.thumb = "off";
        return;
      }
      // Read everything before writing: a style read after a write would
      // start the transition before `instant` switches it off.
      const x = item.offsetLeft;
      const y = item.offsetTop;
      const w = item.offsetWidth;
      const h = item.offsetHeight;
      const r = getComputedStyle(item).borderRadius;
      const { style } = group;
      style.setProperty("--thumb-x", `${x}px`);
      style.setProperty("--thumb-y", `${y}px`);
      style.setProperty("--thumb-w", `${w}px`);
      style.setProperty("--thumb-h", `${h}px`);
      style.setProperty("--thumb-r", r);
      if (slide && group.dataset.thumb === "on") return;

      group.dataset.thumb = "instant";
      cancelAnimationFrame(raf);
      // Two frames: the new geometry must be styled once with transitions off.
      raf = requestAnimationFrame(() => {
        raf = requestAnimationFrame(() => {
          group.dataset.thumb = "on";
        });
      });
    };

    place(false);
    const pressed = new MutationObserver(() => place(true));
    pressed.observe(group, { subtree: true, attributeFilter: ["data-pressed"] });
    const resized = new ResizeObserver(() => place(false));
    for (const el of [group, ...group.children]) resized.observe(el);

    return () => {
      cancelAnimationFrame(raf);
      pressed.disconnect();
      resized.disconnect();
      delete group.dataset.thumb;
    };
  }, [enabled]);

  return ref;
}
