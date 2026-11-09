"use client";

import { useT } from "@/lib/i18n";
import { SCORE_BUCKETS, isBucketKept, summarizeHistogram } from "@/lib/dataset";
import { cn, formatInteger, formatTokenCount } from "@/lib/utils";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";

interface ScoreHistogramProps {
  /** 不应用评分阈值的直方图，键 "0" 为未评分 */
  histogram: Record<string, number> | undefined;
  minScore: number;
  onChange: (minScore: number) => void;
}

/**
 * 质量门槛：直方图本身就是选择器。点第 k 根柱 = 保留 k 分及以上，点未评分柱 = 不限。
 * 阈值以下的柱变淡，被砍掉的量一眼可见。
 */
export function ScoreHistogram({ histogram, minScore, onChange }: ScoreHistogramProps) {
  const t = useT();
  const summary = summarizeHistogram(histogram, minScore);

  const caption =
    minScore === 0
      ? t("dataset.quality_any")
          .replace("{all}", formatInteger(summary.all))
          .replace("{unscored}", formatInteger(summary.unscored))
      : t("dataset.quality_kept")
          .replace("{score}", String(minScore))
          .replace("{kept}", formatInteger(summary.kept))
          .replace("{all}", formatInteger(summary.all));

  return (
    <div className="space-y-2.5">
      <ToggleGroup
        value={[String(minScore)]}
        onValueChange={(value) => {
          const next = value[0];
          if (next !== undefined) onChange(Number(next));
        }}
        aria-label={t("dataset.section_quality")}
        className="grid w-full grid-cols-6 items-end gap-1"
      >
        {SCORE_BUCKETS.map((bucket) => {
          const count = histogram?.[bucket] ?? 0;
          const kept = isBucketKept(bucket, minScore);
          const selected = bucket === minScore;
          const label = (
            bucket === 0
              ? t("dataset.score_any_aria")
              : t("dataset.score_bucket_aria").replace("{score}", String(bucket))
          ).replace("{n}", formatInteger(count));
          return (
            <ToggleGroupItem
              key={bucket}
              value={String(bucket)}
              aria-label={label}
              className="h-auto min-w-0 flex-col items-stretch gap-1 rounded-md px-0 pt-1.5 pb-1 hover:bg-muted/70 aria-pressed:bg-muted/70"
            >
              <span
                className={cn(
                  "font-mono text-[11px] leading-none tabular-nums",
                  kept ? "text-foreground" : "text-muted-foreground",
                )}
              >
                {formatTokenCount(count)}
              </span>
              <span className="flex h-20 items-end px-2">
                <span
                  className={cn(
                    "w-full rounded-t-[3px] transition-[height,background-color] duration-300 ease-out",
                    kept ? "bg-primary" : "bg-muted-foreground/20",
                  )}
                  style={{
                    height: `${count === 0 ? 0 : Math.max(4, (count / summary.peak) * 100)}%`,
                  }}
                />
              </span>
              <span
                className={cn(
                  "mx-1 border-t pt-1 text-center text-[11px] leading-tight tabular-nums",
                  selected
                    ? "border-primary font-semibold text-primary-ink"
                    : "border-border text-muted-foreground",
                )}
              >
                {bucket === 0 ? t("dataset.score_unscored_short") : bucket}
              </span>
            </ToggleGroupItem>
          );
        })}
      </ToggleGroup>
      <p className="text-xs text-muted-foreground tabular-nums">{caption}</p>
    </div>
  );
}
