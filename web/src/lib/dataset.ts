/**
 * 训练数据工作台的纯逻辑：ShareGPT 样本解析、评分直方图、模型分面排序。
 * 不依赖 React，由 dataset.test.ts 守护。
 */

/** 直方图的桶：0 = 未评分，1–5 = 评分 */
export const SCORE_BUCKETS = [0, 1, 2, 3, 4, 5] as const;

export interface ShareGPTTurn {
  from: string;
  /** 去掉 <think> 段落后的正文 */
  value: string;
  /** <think>…</think> 中的推理内容 */
  think: string;
  functionCall?: { name: string; arguments: string };
  /** function 角色的 tool_call_id */
  name?: string;
}

export interface ShareGPTSample {
  turns: ShareGPTTurn[];
  /** 工具定义原样保留，供 JSON 展示 */
  tools: unknown[];
  images: string[];
}

const THINK_RE = /^\s*<think>([\s\S]*?)<\/think>\s*/;

function asString(v: unknown): string {
  return typeof v === "string" ? v : "";
}

/** 解析一行 ShareGPT JSON；不是合法对象或缺 conversations 时返回 null */
export function parseShareGPTLine(line: string): ShareGPTSample | null {
  let raw: unknown;
  try {
    raw = JSON.parse(line);
  } catch {
    return null;
  }
  if (!raw || typeof raw !== "object") return null;
  const obj = raw as Record<string, unknown>;
  if (!Array.isArray(obj.conversations)) return null;

  const turns = obj.conversations.map((item): ShareGPTTurn => {
    const m = (item && typeof item === "object" ? item : {}) as Record<string, unknown>;
    const value = asString(m.value);
    const match = THINK_RE.exec(value);
    const fc = m.function_call as Record<string, unknown> | undefined;
    return {
      from: asString(m.from),
      value: match ? value.slice(match[0].length) : value,
      think: match ? match[1].trim() : "",
      functionCall:
        fc && typeof fc === "object"
          ? { name: asString(fc.name), arguments: asString(fc.arguments) }
          : undefined,
      name: asString(m.name) || undefined,
    };
  });

  return {
    turns,
    tools: Array.isArray(obj.tools) ? obj.tools : [],
    images: Array.isArray(obj.images) ? obj.images.map(asString) : [],
  };
}

/** 可解析则 2 空格缩进，否则原样返回（纯文本渲染，无注入面） */
export function prettyJSON(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

export interface HistogramSummary {
  /** 直方图覆盖的全部会话（不受评分阈值影响） */
  all: number;
  /** 阈值保留下来的会话 */
  kept: number;
  unscored: number;
  /** 最高的桶，用于柱高归一化（至少为 1） */
  peak: number;
}

/** 按阈值汇总直方图：minScore 为 0 时不限评分（含未评分） */
export function summarizeHistogram(
  histogram: Record<string, number> | undefined,
  minScore: number,
): HistogramSummary {
  let all = 0;
  let kept = 0;
  let peak = 1;
  for (const bucket of SCORE_BUCKETS) {
    const n = histogram?.[bucket] ?? 0;
    all += n;
    if (minScore === 0 || (bucket > 0 && bucket >= minScore)) kept += n;
    peak = Math.max(peak, n);
  }
  return { all, kept, unscored: histogram?.[0] ?? 0, peak };
}

/** 柱子是否在阈值保留范围内 */
export function isBucketKept(bucket: number, minScore: number): boolean {
  return minScore === 0 || (bucket > 0 && bucket >= minScore);
}

export interface ModelFacet {
  model: string;
  count: number;
}

/**
 * 模型分面列表：按会话数降序、同数按名称。已勾选但当前范围内没有会话的模型保留（计 0）并置顶——
 * 否则用户既看不到筛空的原因，也无法取消这个勾选。只置顶这一类，正常勾选不改变行位置，
 * 避免点击后行从指针下跳走。
 */
export function buildModelFacets(
  facets: Record<string, number> | undefined,
  selected: string[],
): ModelFacet[] {
  const merged = new Map<string, number>(Object.entries(facets ?? {}));
  for (const m of selected) if (!merged.has(m)) merged.set(m, 0);
  const rank = (f: ModelFacet) => (f.count === 0 && selected.includes(f.model) ? 0 : 1);
  return [...merged]
    .map(([model, count]) => ({ model, count }))
    .sort((a, b) => rank(a) - rank(b) || b.count - a.count || a.model.localeCompare(b.model));
}

/** 随机取一个与当前不同的下标（总数 ≤ 1 时返回 0） */
export function randomOffset(total: number, current: number, random = Math.random): number {
  if (total <= 1) return 0;
  const next = Math.floor(random() * (total - 1));
  return next >= current ? next + 1 : next;
}
