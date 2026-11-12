import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { PortalEcosystem } from "./portal-ecosystem";

vi.mock("@/components/provider-icon", () => ({ ProviderIcon: () => null }));

function renderPanels() {
  const html = renderToStaticMarkup(
    createElement(I18nProvider, null, createElement(PortalEcosystem)),
  );
  return [
    ...html.matchAll(/<section\b[^>]*aria-labelledby="([^"]+)"[^>]*>([\s\S]*?)<\/section>/g),
  ].map(([, titleId, body]) => ({
    title: body.match(new RegExp(`<h3\\b[^>]*id="${titleId}"[^>]*>([^<]*)<\\/h3>`))?.[1],
    rows: [...body.matchAll(/<ul\b([^>]*)>([\s\S]*?)<\/ul>/g)]
      .filter(([, attrs]) => !attrs.includes('aria-hidden="true"'))
      .map(([, , list]) =>
        [...list.matchAll(/<li\b([^>]*)>([\s\S]*?)<\/li>/g)]
          .filter(([, attrs]) => !attrs.includes('aria-hidden="true"'))
          .map(([, , text]) => text.replace(/<[^>]*>/g, "").trim()),
      ),
  }));
}

describe("兼容生态分框", () => {
  it("客户端与模型厂商分别拥有命名区域和两条品牌行", () => {
    const panels = renderPanels();
    expect(panels.map((panel) => panel.title)).toEqual([
      "Supported clients",
      "Supported model vendors",
    ]);
    expect(panels.map((panel) => panel.rows.length)).toEqual([2, 2]);
  });

  it("轮播副本不重复出现在读屏列表中，也不跨框混入其他类别", () => {
    const panels = renderPanels();
    expect(panels[0]?.rows.flat()).toEqual([
      "Claude Code",
      "Codex",
      "OpenCode",
      "Pi",
      "DeepSeek Harness",
    ]);
    expect(panels[1]?.rows.flat()).toEqual([
      "OpenAI",
      "Anthropic",
      "DeepSeek",
      "Qwen",
      "Hy",
      "MiniMax",
      "GLM",
    ]);
  });
});
