import { describe, expect, it } from "vitest";
import { THEME_INIT_SCRIPT, parsePreference, resolveTheme } from "./theme-preference";

/** Runs the first-paint script against a stubbed storage + media query. */
function runInitScript(stored: string | null, systemDark: boolean, storageThrows = false): string {
  const documentElement = { dataset: {} as Record<string, string> };
  const localStorage = {
    getItem: () => {
      if (storageThrows) throw new Error("denied");
      return stored;
    },
  };
  const window = { matchMedia: () => ({ matches: systemDark }) };
  new Function("document", "localStorage", "window", THEME_INIT_SCRIPT)(
    { documentElement },
    localStorage,
    window,
  );
  return documentElement.dataset.theme;
}

describe("resolveTheme", () => {
  it("keeps an explicit skin regardless of the system", () => {
    expect(resolveTheme("anthropic", true)).toBe("anthropic");
    expect(resolveTheme("moonshot", false)).toBe("moonshot");
  });

  it("follows the system appearance", () => {
    expect(resolveTheme("system", true)).toBe("moonshot");
    expect(resolveTheme("system", false)).toBe("anthropic");
  });
});

describe("THEME_INIT_SCRIPT", () => {
  // The inline script is a hand-written mirror of parsePreference + resolveTheme.
  const cases: [string | null, boolean][] = [
    [null, false],
    [null, true],
    ["system", true],
    ["garbage", false],
    ["anthropic", true],
    ["moonshot", false],
  ];
  it.each(cases)("stored=%s systemDark=%s matches resolveTheme", (stored, systemDark) => {
    expect(runInitScript(stored, systemDark)).toBe(
      resolveTheme(parsePreference(stored), systemDark),
    );
  });

  it("falls back to anthropic when storage is unavailable", () => {
    expect(runInitScript(null, true, true)).toBe("anthropic");
  });
});
