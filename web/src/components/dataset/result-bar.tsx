"use client";

import { Check, Download, X } from "lucide-react";

import { useT } from "@/lib/i18n";
import { formatBytes, formatDecimal, formatInteger } from "@/lib/utils";
import type { DatasetPreviewRsp } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Skeleton } from "@/components/ui/skeleton";
import { MetricNumber } from "@/components/motion/metric-number";

export type ExportState =
  | { status: "idle" }
  | { status: "running"; done: number; total: number }
  | { status: "done"; recipe: string; file: string; size: number };

interface ResultBarProps {
  preview: DatasetPreviewRsp | null;
  recipe: string;
  exportState: ExportState;
  onExport: () => void;
  onCancel: () => void;
}

/**
 * 吸顶结果栏：当前配方的唯一结果展示处，导出就在这里原地完成。
 * 导出中底边是进度线，完成后留一行已下载的文件名（配方变了就收起）。
 */
export function ResultBar({ preview, recipe, exportState, onExport, onCancel }: ResultBarProps) {
  const t = useT();
  const total = preview?.totalSessions ?? 0;
  const messages = preview?.totalMessages ?? 0;
  const tools = preview?.toolSessions ?? 0;
  const running = exportState.status === "running";
  const done = exportState.status === "done" && exportState.recipe === recipe ? exportState : null;
  const progress =
    running && exportState.total > 0 ? (exportState.done / exportState.total) * 100 : 0;

  const stats = [
    t("dataset.stat_messages").replace("{n}", formatInteger(messages)),
    t("dataset.stat_avg").replace("{n}", total > 0 ? formatDecimal(messages / total, 1) : "0"),
    t("dataset.stat_tools").replace(
      "{pct}",
      `${total > 0 ? formatDecimal((tools / total) * 100, 0) : "0"}%`,
    ),
  ];

  return (
    <div className="sticky top-0 z-20 -mx-4 border-y border-border bg-background px-4 py-3 md:-mx-8 md:px-8 lg:-mx-10 lg:px-10">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <div className="flex items-baseline gap-2" aria-live="polite">
          {preview ? (
            <span className="font-display text-2xl leading-none text-foreground tabular-nums">
              <MetricNumber value={formatInteger(total)} />
            </span>
          ) : (
            <Skeleton className="h-6 w-16" />
          )}
          <span className="text-sm text-muted-foreground">{t("dataset.sessions_unit")}</span>
        </div>

        {total > 0 && (
          <ul className="hidden items-center text-sm text-muted-foreground tabular-nums sm:flex [&>li+li]:ml-4 [&>li+li]:border-l [&>li+li]:border-border [&>li+li]:pl-4">
            {stats.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ul>
        )}

        <div className="ml-auto flex items-center gap-3">
          {running ? (
            <>
              <span className="font-mono text-xs text-muted-foreground tabular-nums">
                {t("dataset.export_progress")
                  .replace("{done}", formatInteger(exportState.done))
                  .replace("{total}", formatInteger(exportState.total))}
              </span>
              <Button variant="outline" onClick={onCancel}>
                <X data-icon="inline-start" />
                {t("dataset.export_cancel")}
              </Button>
            </>
          ) : (
            <>
              {done && (
                <span className="hidden items-center gap-1.5 text-xs text-muted-foreground md:inline-flex">
                  <Check className="size-3.5 text-positive" />
                  {t("dataset.export_done")
                    .replace("{file}", done.file)
                    .replace("{size}", formatBytes(done.size))}
                </span>
              )}
              <Button onClick={onExport} disabled={total === 0}>
                <Download data-icon="inline-start" />
                {t("dataset.export")}
              </Button>
            </>
          )}
        </div>
      </div>

      {running && (
        <Progress
          value={progress}
          aria-label={t("dataset.export")}
          className="absolute inset-x-0 -bottom-px gap-0 [&_[data-slot=progress-track]]:h-0.5 [&_[data-slot=progress-track]]:rounded-none [&_[data-slot=progress-track]]:bg-primary/15"
        />
      )}
    </div>
  );
}
