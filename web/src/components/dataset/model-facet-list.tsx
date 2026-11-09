"use client";

import { useState } from "react";
import { Search } from "lucide-react";

import { useT } from "@/lib/i18n";
import type { ModelFacet } from "@/lib/dataset";
import { cn, formatInteger } from "@/lib/utils";
import { Checkbox } from "@/components/ui/checkbox";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { TruncatedText } from "@/components/truncated-text";

/** 超过这个数量才出现搜索框：少量模型一眼能扫完 */
const SEARCH_THRESHOLD = 8;

interface ModelFacetListProps {
  facets: ModelFacet[];
  selected: string[];
  onChange: (selected: string[]) => void;
}

/**
 * 模型分面：勾选列表，每行带会话数与比例条。计数不受模型筛选影响，
 * 未勾选的模型也能看到「勾上会多出多少」；多选为「任一」语义。
 */
export function ModelFacetList({ facets, selected, onChange }: ModelFacetListProps) {
  const t = useT();
  const [query, setQuery] = useState("");
  const peak = Math.max(1, ...facets.map((f) => f.count));
  const needle = query.trim().toLowerCase();
  const visible = needle ? facets.filter((f) => f.model.toLowerCase().includes(needle)) : facets;

  const toggle = (model: string, checked: boolean) =>
    onChange(checked ? [...selected, model] : selected.filter((m) => m !== model));

  if (facets.length === 0) {
    return <p className="py-2 text-sm text-muted-foreground">{t("dataset.no_models")}</p>;
  }

  return (
    <div className="space-y-2">
      {facets.length > SEARCH_THRESHOLD && (
        <InputGroup className="h-8">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("dataset.model_search")}
            aria-label={t("dataset.model_search")}
            className="text-sm pointer-coarse:text-base"
          />
        </InputGroup>
      )}
      <ul className="-mx-2 max-h-80 overflow-y-auto">
        {visible.map((facet) => {
          const checked = selected.includes(facet.model);
          return (
            <li key={facet.model}>
              <label className="flex min-h-9 cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 hover:bg-muted/60">
                <Checkbox checked={checked} onCheckedChange={(next) => toggle(facet.model, next)} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-baseline gap-2">
                    <TruncatedText className="min-w-0 flex-1 font-mono text-xs text-foreground">
                      {facet.model}
                    </TruncatedText>
                    <span className="font-mono text-xs text-muted-foreground tabular-nums">
                      {formatInteger(facet.count)}
                    </span>
                  </span>
                  <span aria-hidden className="mt-1 block h-0.5 rounded-full bg-muted">
                    <span
                      className={cn(
                        "block h-full rounded-full transition-[width] duration-300 ease-out",
                        checked ? "bg-primary" : "bg-muted-foreground/35",
                      )}
                      style={{ width: `${(facet.count / peak) * 100}%` }}
                    />
                  </span>
                </span>
              </label>
            </li>
          );
        })}
        {visible.length === 0 && (
          <li className="px-2 py-2 text-sm text-muted-foreground">{t("dataset.no_model_match")}</li>
        )}
      </ul>
    </div>
  );
}
