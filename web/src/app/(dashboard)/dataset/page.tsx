"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Database, RotateCcw } from "lucide-react";
import { toast } from "sonner";

import { usePersistentState } from "@/hooks/use-persistent-state";
import { api } from "@/lib/api-client";
import { showErrorToast } from "@/lib/api-error-handler";
import { buildModelFacets, randomOffset } from "@/lib/dataset";
import {
  collectDatasetDownload,
  DatasetDownloadError,
  openDatasetFile,
  writeDatasetDownload,
} from "@/lib/dataset-download";
import { useT } from "@/lib/i18n";
import { computeRange, type TimeRangeKey } from "@/lib/time-range";
import type { DatasetFormatPreviewRsp, DatasetPreviewRsp, DatasetRecipe } from "@/lib/types";
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

/** 加载结果绑定请求对象身份，切回同样的筛选条件也要等新的响应。 */
interface Keyed<Key, T> {
  key: Key;
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

  const params = useMemo<DatasetRecipe>(() => {
    const { startTime, endTime } = computeRange(timeRange, customStart, customEnd);
    return { minScore, modelIds: models, startTime, endTime };
  }, [minScore, models, timeRange, customStart, customEnd]);

  const [preview, setPreview] = useState<Keyed<DatasetRecipe, DatasetPreviewRsp> | null>(null);
  const [cursor, setCursor] = useState<Keyed<DatasetRecipe, number> | null>(null);
  const offset = cursor?.key === params ? cursor.data : 0;
  const sampleKey = useMemo(() => ({ params, offset }), [params, offset]);
  const [sample, setSample] = useState<Keyed<
    typeof sampleKey,
    DatasetFormatPreviewRsp | null
  > | null>(null);
  const [exportState, setExportState] = useState<ExportState>({ status: "idle" });
  const abortRef = useRef<AbortController | null>(null);

  const currentPreview = preview?.key === params ? preview.data : null;
  const total = currentPreview?.totalSessions ?? 0;
  const hasMatches = total > 0;
  const currentSample = hasMatches && sample?.key === sampleKey ? sample : null;

  useEffect(() => {
    let active = true;
    api
      .previewDataset(params)
      .then((rsp) => {
        if (!active) return;
        if (rsp.error) {
          showErrorToast(rsp.error, { title: t("dataset.preview_error") });
          return;
        }
        setPreview({ key: params, data: rsp });
      })
      .catch((err) => {
        if (active) showErrorToast(err, { title: t("dataset.preview_error") });
      });
    return () => {
      active = false;
    };
  }, [params, t]);

  useEffect(() => {
    if (!hasMatches) return;
    let active = true;
    const fail = (err: unknown) => {
      if (!active) return;
      setSample({ key: sampleKey, data: null });
      showErrorToast(err, { title: t("dataset.sample_error") });
    };
    api
      .previewDatasetFormat({ ...params, offset })
      .then((rsp) => {
        if (!active) return;
        if (rsp.error) {
          fail(rsp.error);
          return;
        }
        setSample({ key: sampleKey, data: rsp });
      })
      .catch(fail);
    return () => {
      active = false;
    };
  }, [params, offset, sampleKey, hasMatches, t]);

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
    setCursor({ key: params, data: Math.min(Math.max(next, 0), total - 1) });

  const handleExport = async () => {
    if (!hasMatches || abortRef.current) return;
    const controller = new AbortController();
    const { signal } = controller;
    const file = `aris-dataset-${new Date().toISOString().slice(0, 10)}.jsonl`;
    abortRef.current = controller;
    setExportState({ status: "running", done: 0, total });
    let pendingFile: Awaited<ReturnType<typeof openDatasetFile>> = null;
    try {
      pendingFile = await openDatasetFile(file);
      signal.throwIfAborted();
      if (!pendingFile) toast.info(t("dataset.export_memory_notice"));
      const result = await api.exportDatasetStream(params, signal);
      const onProgress = (done: number, all: number) =>
        setExportState({ status: "running", done, total: all });
      let size: number;
      if (pendingFile) {
        const writer = pendingFile;
        pendingFile = null;
        size = await writeDatasetDownload(result.body, result.total, writer, onProgress, signal);
      } else {
        const blob = await collectDatasetDownload(result.body, result.total, onProgress, signal);
        signal.throwIfAborted();
        const url = URL.createObjectURL(blob);
        const link = document.createElement("a");
        link.href = url;
        link.download = file;
        document.body.appendChild(link);
        link.click();
        link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
        size = blob.size;
      }
      setExportState({ status: "done", recipe: params, file, size });
      toast.success(t("dataset.export_success"));
    } catch (err) {
      setExportState({ status: "idle" });
      if (signal.aborted || (err instanceof DOMException && err.name === "AbortError")) {
        toast(t("dataset.export_cancelled"));
      } else if (err instanceof DatasetDownloadError) {
        const failure = err.failure;
        let description: string;
        switch (failure.reason) {
          case "memory_limit":
            description = t("dataset.export_memory_error");
            break;
          case "session_too_large":
            description = t("dataset.export_session_too_large").replace("{id}", failure.sessionId);
            break;
          case "incomplete":
            description = t("dataset.export_incomplete")
              .replace("{received}", formatInteger(failure.lines))
              .replace("{total}", formatInteger(failure.total));
            break;
        }
        showErrorToast(err, { title: t("dataset.export_error"), description });
      } else {
        showErrorToast(err, { title: t("dataset.export_error") });
      }
    } finally {
      await pendingFile?.abort().catch(() => {});
      abortRef.current = null;
    }
  };

  const facets = buildModelFacets(currentPreview?.modelFacets, models);
  const histogram = currentPreview?.scoreHistogram;
  const previewLoaded = currentPreview !== null;

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
            preview={currentPreview}
            recipe={params}
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
                  sample={currentSample?.data ?? null}
                  loading={currentSample === null}
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
