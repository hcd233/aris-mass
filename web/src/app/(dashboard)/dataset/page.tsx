"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Database, RotateCcw } from "lucide-react";
import { toast } from "sonner";

import { usePersistentState } from "@/hooks/use-persistent-state";
import { useRequestSeq } from "@/hooks/use-request-seq";
import { api } from "@/lib/api-client";
import { showErrorToast } from "@/lib/api-error-handler";
import { buildModelFacets, randomOffset } from "@/lib/dataset";
import { useT } from "@/lib/i18n";
import { computeRange, type TimeRangeKey } from "@/lib/time-range";
import type { DatasetFormatPreviewRsp, DatasetPreviewRsp } from "@/lib/types";
import { formatInteger } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { TimeRangePicker } from "@/components/ui/time-range-picker";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ListEmptyState } from "@/components/list-empty-state";
import { PageHeader } from "@/components/page-header";
import { PermissionGuard } from "@/components/permission-guard";
import { ModelFacetList } from "@/components/dataset/model-facet-list";
import { ResultBar, type ExportState } from "@/components/dataset/result-bar";
import { SampleInspector, type SampleView } from "@/components/dataset/sample-inspector";
import { ScoreHistogram } from "@/components/dataset/score-histogram";

const DEFAULT_TIME_RANGE: TimeRangeKey = "30d";

/** 一次加载结果连同它所属的配方，配方变化后旧结果自动失效（无需在 effect 里清空） */
interface Keyed<T> {
  key: string;
  data: T;
}

function RecipeSection({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="space-y-3 px-5 py-5">
      <div>
        <h2 className="text-sm font-medium text-foreground">{title}</h2>
        {hint && <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p>}
      </div>
      {children}
    </section>
  );
}

export default function DatasetPage() {
  const t = useT();
  const [minScore, setMinScore] = usePersistentState("dashboard.dataset.minScore", 3);
  const [models, setModels] = usePersistentState<string[]>("dashboard.dataset.models", []);
  const [timeRange, setTimeRange] = usePersistentState<TimeRangeKey>(
    "dashboard.dataset.timeRange",
    DEFAULT_TIME_RANGE,
  );
  const [customStart, setCustomStart] = usePersistentState("dashboard.dataset.customStart", "");
  const [customEnd, setCustomEnd] = usePersistentState("dashboard.dataset.customEnd", "");
  const [view, setView] = usePersistentState<SampleView>("dashboard.dataset.view", "conversation");

  const recipe = JSON.stringify([minScore, models, timeRange, customStart, customEnd]);
  const params = useMemo(() => {
    const { startTime, endTime } = computeRange(timeRange, customStart, customEnd);
    return { minScore, modelIds: models, startTime, endTime };
  }, [minScore, models, timeRange, customStart, customEnd]);

  const [preview, setPreview] = useState<Keyed<DatasetPreviewRsp> | null>(null);
  const [cursor, setCursor] = useState<Keyed<number>>({ key: recipe, data: 0 });
  const [sample, setSample] = useState<Keyed<DatasetFormatPreviewRsp> | null>(null);
  const [sampleLoading, setSampleLoading] = useState(false);
  const [exportState, setExportState] = useState<ExportState>({ status: "idle" });
  const abortRef = useRef<AbortController | null>(null);
  const previewGuard = useRequestSeq();
  const sampleGuard = useRequestSeq();

  const offset = cursor.key === recipe ? cursor.data : 0;
  const currentPreview = preview?.key === recipe ? preview.data : null;
  const total = currentPreview?.totalSessions ?? 0;
  const sampleKey = `${recipe}#${offset}`;
  const hasMatches = total > 0;

  /* eslint-disable react-hooks/set-state-in-effect -- 配方变化后重新拉取服务端统计与样本 */
  useEffect(() => {
    const seq = previewGuard.begin();
    const key = recipe;
    api
      .previewDataset(params)
      .then((rsp) => {
        if (!previewGuard.isCurrent(seq)) return;
        if (rsp.error) {
          showErrorToast(rsp.error, { title: t("dataset.preview_error") });
          return;
        }
        setPreview({ key, data: rsp });
      })
      .catch((err) => {
        if (previewGuard.isCurrent(seq)) showErrorToast(err, { title: t("dataset.preview_error") });
      });
  }, [params, recipe, previewGuard, t]);

  useEffect(() => {
    if (!hasMatches) return;
    const seq = sampleGuard.begin();
    setSampleLoading(true);
    api
      .previewDatasetFormat({ ...params, offset })
      .then((rsp) => {
        if (!sampleGuard.isCurrent(seq)) return;
        if (rsp.error) {
          showErrorToast(rsp.error, { title: t("dataset.sample_error") });
          return;
        }
        setSample({ key: sampleKey, data: rsp });
      })
      .catch((err) => {
        if (sampleGuard.isCurrent(seq)) showErrorToast(err, { title: t("dataset.sample_error") });
      })
      .finally(() => {
        if (sampleGuard.isCurrent(seq)) setSampleLoading(false);
      });
  }, [params, offset, sampleKey, hasMatches, sampleGuard, t]);
  /* eslint-enable react-hooks/set-state-in-effect */

  useEffect(() => () => abortRef.current?.abort(), []);

  const activeFilters = minScore > 0 || models.length > 0 || timeRange !== DEFAULT_TIME_RANGE;

  const resetRecipe = () => {
    setMinScore(0);
    setModels([]);
    setTimeRange(DEFAULT_TIME_RANGE);
    setCustomStart("");
    setCustomEnd("");
  };

  const goTo = (next: number) =>
    setCursor({ key: recipe, data: Math.min(Math.max(next, 0), total - 1) });

  const handleExport = async () => {
    if (abortRef.current) return;
    const controller = new AbortController();
    abortRef.current = controller;
    setExportState({ status: "running", done: 0, total });
    try {
      const result = await api.exportDatasetStream(
        params,
        (done, all) => setExportState({ status: "running", done, total: all }),
        controller.signal,
      );
      if (result.lines < result.total) {
        throw new Error(
          t("dataset.export_incomplete")
            .replace("{received}", formatInteger(result.lines))
            .replace("{total}", formatInteger(result.total)),
        );
      }
      const blob = new Blob(result.chunks as BlobPart[], { type: "application/x-ndjson" });
      const file = `aris-dataset-${new Date().toISOString().slice(0, 10)}.jsonl`;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = file;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      setExportState({ status: "done", recipe, file, size: blob.size });
      toast.success(t("dataset.export_success"));
    } catch (err) {
      setExportState({ status: "idle" });
      if (controller.signal.aborted) {
        toast(t("dataset.export_cancelled"));
      } else {
        showErrorToast(err, { title: t("dataset.export_error") });
      }
    } finally {
      abortRef.current = null;
    }
  };

  const facets = buildModelFacets(currentPreview?.modelFacets ?? preview?.data.modelFacets, models);
  const histogram = currentPreview?.scoreHistogram ?? preview?.data.scoreHistogram;
  const previewLoaded = currentPreview !== null;
  const shownSample = hasMatches && sample ? sample.data : null;

  return (
    <PermissionGuard>
      <TooltipProvider>
        <div className="space-y-6">
          <PageHeader
            title={t("dataset.title")}
            description={t("dataset.subtitle")}
            actions={
              <TimeRangePicker
                value={timeRange}
                customStart={customStart}
                customEnd={customEnd}
                onChange={(key, start, end) => {
                  setTimeRange(key);
                  setCustomStart(start);
                  setCustomEnd(end);
                }}
              />
            }
          />

          <ResultBar
            preview={currentPreview ?? preview?.data ?? null}
            recipe={recipe}
            exportState={exportState}
            onExport={handleExport}
            onCancel={() => abortRef.current?.abort()}
          />

          <div className="grid items-start gap-6 lg:grid-cols-[20rem_minmax(0,1fr)]">
            <Card className="gap-0 divide-y divide-border py-0">
              <RecipeSection title={t("dataset.section_quality")}>
                <ScoreHistogram histogram={histogram} minScore={minScore} onChange={setMinScore} />
              </RecipeSection>
              <RecipeSection title={t("dataset.section_models")} hint={t("dataset.models_any")}>
                <ModelFacetList facets={facets} selected={models} onChange={setModels} />
              </RecipeSection>
              {activeFilters && (
                <div className="px-3 py-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={resetRecipe}
                    className="text-muted-foreground"
                  >
                    <RotateCcw data-icon="inline-start" />
                    {t("dataset.reset")}
                  </Button>
                </div>
              )}
            </Card>

            <Card className="min-h-96 gap-0 py-0">
              {previewLoaded && !hasMatches ? (
                <div className="flex flex-1 items-center justify-center py-16">
                  <ListEmptyState
                    icon={Database}
                    message={t("dataset.no_data")}
                    hint={t("dataset.empty_hint")}
                    action={
                      activeFilters && (
                        <Button variant="outline" onClick={resetRecipe}>
                          <RotateCcw data-icon="inline-start" />
                          {t("dataset.clear_filters")}
                        </Button>
                      )
                    }
                  />
                </div>
              ) : (
                <SampleInspector
                  sample={shownSample}
                  loading={sampleLoading || sample?.key !== sampleKey}
                  offset={offset}
                  total={total}
                  view={view}
                  onViewChange={setView}
                  onPrev={() => goTo(offset - 1)}
                  onNext={() => goTo(offset + 1)}
                  onRandom={() => goTo(randomOffset(total, offset))}
                />
              )}
            </Card>
          </div>
        </div>
      </TooltipProvider>
    </PermissionGuard>
  );
}
