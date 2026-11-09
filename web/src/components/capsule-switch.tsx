"use client";

import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { TooltipContent, TooltipRoot, TooltipTrigger } from "@/components/ui/tooltip";
import { centerOf, type Point } from "@/lib/motion/theme-transition";
import { cn } from "@/lib/utils";

export interface CapsuleOption<T extends string> {
  value: T;
  /** Accessible name, also shown as the tooltip. */
  label: string;
  /** Short glyph or icon rendered inside the cell. */
  content: ReactNode;
}

/**
 * Pill-shaped single-choice switch (radiogroup): equal-width cells, a thumb
 * that slides to the chosen one, roving tabindex with ← / → / Home / End.
 * `onChange` receives the chosen cell's center so callers can start their
 * transition from where the user clicked.
 */
export function CapsuleSwitch<T extends string>({
  value,
  options,
  onChange,
  label,
  className,
}: {
  value: T;
  options: readonly CapsuleOption<T>[];
  onChange: (value: T, origin: Point) => void;
  label: string;
  className?: string;
}) {
  const cellRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const index = Math.max(
    0,
    options.findIndex((o) => o.value === value),
  );

  const pick = (i: number) => {
    const cell = cellRefs.current[i];
    if (!cell) return;
    cell.focus();
    if (options[i].value !== value) onChange(options[i].value, centerOf(cell));
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const last = options.length - 1;
    const next = index === last ? 0 : index + 1;
    const prev = index === 0 ? last : index - 1;
    const targets: Record<string, number> = {
      ArrowRight: next,
      ArrowDown: next,
      ArrowLeft: prev,
      ArrowUp: prev,
      Home: 0,
      End: last,
    };
    const target = targets[event.key];
    if (target === undefined) return;
    event.preventDefault();
    pick(target);
  };

  return (
    <div
      role="radiogroup"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={cn(
        "relative inline-grid shrink-0 auto-cols-[1.875rem] grid-flow-col rounded-full bg-sidebar-foreground/[0.07] p-0.5",
        className,
      )}
    >
      {/* 滑块的过渡曲线随动效世界走（纸与墨不回弹 / 月光弹簧），写在 motion-*.css 的 .capsule-thumb */}
      <span
        aria-hidden="true"
        className="capsule-thumb absolute top-0.5 left-0.5 h-6.5 w-7.5 rounded-full bg-sidebar shadow-[0_1px_2px_rgb(0_0_0/0.14),0_0_0_1px_color-mix(in_srgb,var(--sidebar-foreground)_8%,transparent)]"
        style={{ transform: `translateX(${index * 100}%)` }}
      />
      {options.map((option, i) => {
        const checked = i === index;
        return (
          <TooltipRoot key={option.value}>
            <TooltipTrigger
              render={
                <button
                  ref={(el) => {
                    cellRefs.current[i] = el;
                  }}
                  type="button"
                  role="radio"
                  aria-checked={checked}
                  aria-label={option.label}
                  tabIndex={checked ? 0 : -1}
                  onClick={() => pick(i)}
                  className={cn(
                    "relative z-10 grid h-6.5 place-items-center rounded-full text-xs font-semibold outline-hidden transition-colors focus-visible:ring-2 focus-visible:ring-sidebar-ring [&_svg]:size-3.75",
                    checked
                      ? "text-sidebar-foreground"
                      : "text-sidebar-foreground/50 hover:text-sidebar-foreground/80",
                  )}
                />
              }
            >
              {option.content}
            </TooltipTrigger>
            <TooltipContent side="top">{option.label}</TooltipContent>
          </TooltipRoot>
        );
      })}
    </div>
  );
}
