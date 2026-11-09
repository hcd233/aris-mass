import { readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/* Motion Grammar 源码契约（styles/motion-*.css）。 */

const SRC = fileURLToPath(new URL("../..", import.meta.url));
const STYLES = join(SRC, "styles");
const sheets = [
  ...readdirSync(STYLES)
    .filter((f) => f.endsWith(".css"))
    .map((f) => join(STYLES, f)),
  join(SRC, "app/globals.css"),
];
const read = (p: string) => readFileSync(p, "utf8");

describe("motion grammar", () => {
  // 同名 @keyframes 不报错，后加载的整段覆盖前者：atelier-stamp 曾在 motion-anthropic
  // （复选框落印）与 motion-signature（登录封面压印）各定义一次，复选框实际播的是登录页的缩放。
  it("@keyframes 名在所有样式文件中唯一", () => {
    const seen = new Map<string, string>();
    const dupes: string[] = [];
    for (const file of sheets) {
      for (const [, name] of read(file).matchAll(/@keyframes\s+([\w-]+)/g)) {
        const prev = seen.get(name);
        if (prev) dupes.push(`${name}: ${prev} ↔ ${relative(SRC, file)}`);
        else seen.set(name, relative(SRC, file));
      }
    }
    expect(dupes).toEqual([]);
  });

  // 状态切换（勾选 / 取消、分段、按钮状态）两套世界必须同时覆盖，只补一边会让另一主题硬切。
  const worlds = ["motion-anthropic.css", "motion-moonshot.css"].map((f) => read(join(STYLES, f)));
  it.each([
    '[data-slot="checkbox-indicator"]',
    '[data-slot="dropdown-menu-radio-item-indicator"] > span',
    '[data-slot="radio-group-indicator"]',
    '[data-thumb="on"]',
    ".capsule-thumb",
    '[data-slot="state-swap"]',
  ])("两套世界都定义 %s", (hook) => {
    for (const css of worlds) expect(css).toContain(hook);
  });

  it("勾选指示器不用挂载 keyframe（菜单展开时已选中的 ✓ 不应重播），退场写在指示器自身", () => {
    for (const css of worlds) {
      const ticks = css.slice(css.indexOf('[data-slot="checkbox-indicator"]'));
      expect(ticks.slice(0, ticks.indexOf("\n}\n"))).not.toMatch(/animation:/);
      expect(ticks).toContain("&[data-starting-style]");
      expect(ticks).toContain("&[data-ending-style] {");
    }
  });
});
