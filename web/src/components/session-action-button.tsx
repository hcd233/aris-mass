"use client";

import { useState, type ComponentProps } from "react";
import { Check, type LucideIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type SessionActionButtonProps = Omit<ComponentProps<typeof Button>, "children" | "variant"> & {
  active: boolean;
  icon: LucideIcon;
  iconClassName?: string;
};

export function SessionActionButton({
  active,
  icon: Icon,
  iconClassName,
  className,
  size = "icon-sm",
  onClick,
  ...props
}: SessionActionButtonProps) {
  const [interacted, setInteracted] = useState(false);

  return (
    <Button
      {...props}
      data-slot="button"
      variant="ghost"
      size={size}
      className={cn("relative", className)}
      onClick={(event) => {
        setInteracted(true);
        onClick?.(event);
      }}
    >
      <Icon
        data-icon="inline-start"
        className={cn("text-muted-foreground", iconClassName)}
        aria-hidden="true"
      />
      <span
        data-slot="session-action-status"
        data-active={active}
        data-animate={interacted || undefined}
        aria-hidden="true"
        className="pointer-events-none absolute -top-0.5 -right-0.5 grid size-3 place-items-center rounded-full border border-confirmation/55 bg-card text-confirmation ring-[1.5px] ring-card"
      >
        <Check className="size-2" strokeWidth={3} />
      </span>
    </Button>
  );
}
