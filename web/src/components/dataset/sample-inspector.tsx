"use client";

import Link from "next/link";
import { ChevronDown, ChevronLeft, ChevronRight, Shuffle } from "lucide-react";

import { useT } from "@/lib/i18n";
import { SESSION_DETAIL_PATH } from "@/lib/routes";
import { parseShareGPTLine, prettyJSON, type ShareGPTTurn } from "@/lib/dataset";
import { cn, formatDateTime, formatInteger } from "@/lib/utils";
import type { DatasetFormatPreviewRsp } from "@/lib/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { HighlightedCode } from "@/components/chat/highlighted-code";
import {
  FoldedContent,
  MESSAGE_FOLD_LINES,
  SYSTEM_FOLD_LINES,
} from "@/components/chat/folded-content";
import { Reasoning } from "@/components/chat/reasoning";
import { TruncatedText } from "@/components/truncated-text";

export type SampleView = "conversation" | "json";

/** ShareGPT 角色的文字色：assistant 是训练目标，用品牌色标出 */
const ROLE_TONE: Record<string, string> = {
  system: "text-muted-foreground",
  user: "text-foreground",
  assistant: "text-primary-ink",
  function: "text-muted-foreground",
};

function looksLikeJSON(text: string): boolean {
  const s = text.trimStart();
  return s.startsWith("{") || s.startsWith("[");
}

function Turn({ turn }: { turn: ShareGPTTurn }) {
  const t = useT();
  const isTool = turn.from === "function";
  return (
    <li className="grid gap-x-6 gap-y-1.5 py-4 md:grid-cols-[7rem_minmax(0,1fr)]">
      <div className="min-w-0 font-mono text-xs leading-6">
        <span className={ROLE_TONE[turn.from] ?? "text-foreground"}>{turn.from}</span>
        {turn.name && (
          <TruncatedText className="block text-[11px] text-muted-foreground" align="start">
            {turn.name}
          </TruncatedText>
        )}
      </div>
      <div className="min-w-0 space-y-3">
        {turn.think && <Reasoning text={turn.think} streaming={false} />}
        {turn.value &&
          (isTool && looksLikeJSON(turn.value) ? (
            <FoldedContent lines={MESSAGE_FOLD_LINES}>
              <HighlightedCode code={prettyJSON(turn.value)} language="json" size="sm" wrap />
            </FoldedContent>
          ) : (
            <FoldedContent lines={turn.from === "system" ? SYSTEM_FOLD_LINES : MESSAGE_FOLD_LINES}>
              <p
                className={cn(
                  "text-sm leading-6 break-words whitespace-pre-wrap",
                  isTool ? "font-mono text-xs text-muted-foreground" : "text-foreground",
                )}
              >
                {turn.value}
              </p>
            </FoldedContent>
          ))}
        {turn.functionCall && (
          <HighlightedCode
            title={turn.functionCall.name}
            code={prettyJSON(turn.functionCall.arguments)}
            language="json"
            size="sm"
            wrap
          />
        )}
        {!turn.value && !turn.functionCall && !turn.think && (
          <p className="text-sm text-muted-foreground">{t("dataset.sample_empty_value")}</p>
        )}
      </div>
    </li>
  );
}

interface SampleInspectorProps {
  sample: DatasetFormatPreviewRsp | null;
  loading: boolean;
  offset: number;
  total: number;
  view: SampleView;
  onViewChange: (view: SampleView) => void;
  onPrev: () => void;
  onNext: () => void;
  onRandom: () => void;
}

/** 样本检查器：逐段翻看匹配的会话，按 ShareGPT 原字段渲染，或切到原始 JSON 行。 */
export function SampleInspector({
  sample,
  loading,
  offset,
  total,
  view,
  onViewChange,
  onPrev,
  onNext,
  onRandom,
}: SampleInspectorProps) {
  const t = useT();
  const line = sample?.sharegptJson ?? "";
  const parsed = line ? parseShareGPTLine(line) : null;
  const showJSON = view === "json" || (line !== "" && !parsed);

  return (
    <section aria-label={t("dataset.view_conversation")} className="flex min-w-0 flex-col">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border px-4 py-3 md:px-5">
        <div className="flex items-center gap-1">
          <Button
            variant="outline"
            size="icon-sm"
            onClick={onPrev}
            disabled={offset <= 0}
            aria-label={t("dataset.sample_prev")}
          >
            <ChevronLeft />
          </Button>
          <Button
            variant="outline"
            size="icon-sm"
            onClick={onNext}
            disabled={offset >= total - 1}
            aria-label={t("dataset.sample_next")}
          >
            <ChevronRight />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onRandom}
            disabled={total <= 1}
            aria-label={t("dataset.sample_random")}
          >
            <Shuffle />
          </Button>
        </div>
        <span className="font-mono text-xs text-muted-foreground tabular-nums" aria-live="polite">
          {t("dataset.sample_position")
            .replace("{current}", formatInteger(offset + 1))
            .replace("{total}", formatInteger(total))}
        </span>
        <ToggleGroup
          variant="segmented"
          size="sm"
          spacing={0}
          value={[view]}
          onValueChange={(value) => {
            const next = value[0];
            if (next === "conversation" || next === "json") onViewChange(next);
          }}
          className="ml-auto"
        >
          <ToggleGroupItem value="conversation">{t("dataset.view_conversation")}</ToggleGroupItem>
          <ToggleGroupItem value="json">{t("dataset.view_json")}</ToggleGroupItem>
        </ToggleGroup>
      </div>

      {sample ? (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-b border-border px-4 py-2.5 text-xs text-muted-foreground md:px-5">
          <Link
            href={`${SESSION_DETAIL_PATH}?id=${sample.sessionId}`}
            className="font-mono text-foreground underline decoration-border underline-offset-4 hover:decoration-foreground"
            title={t("dataset.sample_open")}
          >
            #{sample.sessionId}
          </Link>
          <span>
            {sample.score
              ? t("dataset.sample_score").replace("{score}", String(sample.score))
              : t("dataset.score_unscored")}
          </span>
          {sample.createdAt && (
            <time dateTime={sample.createdAt}>{formatDateTime(sample.createdAt)}</time>
          )}
          {(sample.modelIds ?? []).map((model) => (
            <Badge key={model} variant="outline" className="font-mono">
              {model}
            </Badge>
          ))}
          {parsed && parsed.images.length > 0 && (
            <span>{t("dataset.sample_images").replace("{n}", String(parsed.images.length))}</span>
          )}
        </div>
      ) : (
        <div className="border-b border-border px-4 py-2.5 md:px-5">
          <Skeleton className="h-4 w-64 max-w-full" />
        </div>
      )}

      <div
        className={cn(
          "min-w-0 px-4 transition-opacity duration-200 md:px-5",
          loading && sample && "opacity-50",
        )}
        aria-busy={loading}
      >
        {!sample ? (
          <div className="space-y-4 py-5">
            {[0, 1, 2].map((i) => (
              <div key={i} className="grid gap-4 md:grid-cols-[7rem_minmax(0,1fr)]">
                <Skeleton className="h-4 w-16" />
                <Skeleton className="h-16 w-full" />
              </div>
            ))}
          </div>
        ) : showJSON ? (
          <div className="space-y-3 py-4">
            {view !== "json" && (
              <p className="text-xs text-muted-foreground">{t("dataset.sample_unparsed")}</p>
            )}
            <HighlightedCode code={prettyJSON(line)} language="json" size="sm" wrap />
          </div>
        ) : (
          parsed && (
            <>
              <ol className="divide-y divide-border">
                {parsed.turns.map((turn, i) => (
                  <Turn key={i} turn={turn} />
                ))}
              </ol>
              {parsed.tools.length > 0 && (
                <Collapsible className="border-t border-border py-3">
                  <CollapsibleTrigger className="flex min-h-8 items-center gap-1.5 rounded-md text-xs font-medium text-muted-foreground hover:text-foreground">
                    <ChevronDown className="size-3.5 transition-transform in-data-panel-open:rotate-180" />
                    {t("dataset.sample_tools").replace("{n}", String(parsed.tools.length))}
                  </CollapsibleTrigger>
                  <CollapsibleContent className="pt-2">
                    <HighlightedCode
                      code={JSON.stringify(parsed.tools, null, 2)}
                      language="json"
                      size="sm"
                      wrap
                    />
                  </CollapsibleContent>
                </Collapsible>
              )}
            </>
          )
        )}
      </div>
    </section>
  );
}
