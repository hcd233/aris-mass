import type { ReactNode } from "react";

/**
 * Two states of one control (copy → copied, idle → pending) stacked in a
 * single grid cell: the control keeps the wider state's width, so swapping
 * never resizes it, and the Motion Grammar cross-fades the two instead of
 * snapping (`[data-slot="state-swap"]` in styles/motion-*.css). The hidden
 * state is aria-hidden. Text labels go through <LocaleStableText> so the
 * width also holds across locales.
 */
export function StateSwap({
  active,
  on,
  off,
}: {
  active: boolean;
  /** Shown while `active`. */
  on: ReactNode;
  off: ReactNode;
}) {
  return (
    <span data-slot="state-swap">
      <span aria-hidden={active || undefined}>{off}</span>
      <span aria-hidden={!active || undefined}>{on}</span>
    </span>
  );
}
