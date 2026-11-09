import { describe, expect, it } from "vitest";

import {
  buildModelFacets,
  isBucketKept,
  parseShareGPTLine,
  randomOffset,
  summarizeHistogram,
} from "./dataset";

describe("parseShareGPTLine", () => {
  it("splits <think> out of the value and keeps function calls", () => {
    const sample = parseShareGPTLine(
      JSON.stringify({
        conversations: [
          { from: "user", value: "hi" },
          {
            from: "assistant",
            value: "<think>plan it</think>\n\nanswer",
            function_call: { name: "search", arguments: '{"q":"x"}' },
          },
          { from: "function", value: "result", name: "call_1" },
        ],
        tools: [{ name: "search" }],
        images: ["<image/png, 3 KiB>"],
      }),
    );
    expect(sample?.turns).toEqual([
      { from: "user", value: "hi", think: "", functionCall: undefined, name: undefined },
      {
        from: "assistant",
        value: "answer",
        think: "plan it",
        functionCall: { name: "search", arguments: '{"q":"x"}' },
        name: undefined,
      },
      { from: "function", value: "result", think: "", functionCall: undefined, name: "call_1" },
    ]);
    expect(sample?.tools).toHaveLength(1);
    expect(sample?.images).toEqual(["<image/png, 3 KiB>"]);
  });

  it("only treats a leading <think> block as reasoning", () => {
    const sample = parseShareGPTLine(
      JSON.stringify({ conversations: [{ from: "user", value: "say <think>x</think>" }] }),
    );
    expect(sample?.turns[0]).toMatchObject({ value: "say <think>x</think>", think: "" });
  });

  it("returns null for invalid JSON or a missing conversations array", () => {
    expect(parseShareGPTLine("{oops")).toBeNull();
    expect(parseShareGPTLine('{"tools":[]}')).toBeNull();
    expect(parseShareGPTLine("null")).toBeNull();
  });
});

describe("summarizeHistogram", () => {
  const histogram = { "0": 10, "1": 2, "3": 5, "5": 1 };

  it("keeps everything, unscored included, when minScore is 0", () => {
    expect(summarizeHistogram(histogram, 0)).toEqual({ all: 18, kept: 18, unscored: 10, peak: 10 });
  });

  it("keeps only scored buckets at or above the threshold", () => {
    expect(summarizeHistogram(histogram, 3)).toMatchObject({ all: 18, kept: 6 });
    expect(summarizeHistogram(histogram, 1).kept).toBe(8);
  });

  it("never divides by zero on an empty histogram", () => {
    expect(summarizeHistogram(undefined, 3)).toEqual({ all: 0, kept: 0, unscored: 0, peak: 1 });
  });

  it("matches isBucketKept", () => {
    expect(isBucketKept(0, 0)).toBe(true);
    expect(isBucketKept(0, 1)).toBe(false);
    expect(isBucketKept(2, 3)).toBe(false);
    expect(isBucketKept(4, 3)).toBe(true);
  });
});

describe("buildModelFacets", () => {
  it("pins only selected models with no sessions; normal rows keep count order", () => {
    expect(buildModelFacets({ b: 3, a: 3, c: 9 }, ["gone", "a"])).toEqual([
      { model: "gone", count: 0 },
      { model: "c", count: 9 },
      { model: "a", count: 3 },
      { model: "b", count: 3 },
    ]);
  });
});

describe("randomOffset", () => {
  it("never returns the current offset", () => {
    expect(randomOffset(5, 2, () => 0.5)).toBe(3);
    expect(randomOffset(5, 2, () => 0)).toBe(0);
    expect(randomOffset(5, 4, () => 0.99)).toBe(3);
  });

  it("returns 0 when there is nothing else to pick", () => {
    expect(randomOffset(1, 0)).toBe(0);
    expect(randomOffset(0, 0)).toBe(0);
  });
});
