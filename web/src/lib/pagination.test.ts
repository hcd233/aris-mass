import { describe, expect, it } from "vitest";

import { clampPage } from "./pagination";

describe("clampPage", () => {
  it("keeps a page within the total page count", () => {
    expect(clampPage(3, 45, 20)).toBe(3);
  });

  it("clamps a persisted page past the last page", () => {
    expect(clampPage(5, 21, 20)).toBe(2);
  });

  it("falls back to the first page when there is no data", () => {
    expect(clampPage(4, 0, 20)).toBe(1);
  });

  it("treats invalid page and page size defensively", () => {
    expect(clampPage(Number.NaN, 100, 20)).toBe(1);
    expect(clampPage(2, 100, 0)).toBe(2);
  });

  it("never returns a page below 1", () => {
    expect(clampPage(-3, 100, 20)).toBe(1);
    expect(clampPage(0, 100, 20)).toBe(1);
  });
});
