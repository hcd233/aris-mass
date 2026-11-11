import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DemoAddButton } from "./demo-add-button";
import { I18nProvider } from "../lib/i18n";

const role = vi.hoisted(() => ({ admin: true }));
vi.mock("@/lib/auth-context", () => ({
  useAuth: () => ({ isAdmin: () => role.admin }),
}));

function render(inDemo: boolean, pending = false, loginEnabled = true) {
  return renderToStaticMarkup(
    createElement(
      I18nProvider,
      null,
      createElement(DemoAddButton, {
        sessionId: 42,
        inDemo,
        pending,
        loginEnabled,
        onToggle: () => {},
      }),
    ),
  );
}

function mainIcon(html: string) {
  return html.match(/<svg\b[^>]*>[\s\S]*?<\/svg>/)?.[0];
}

beforeEach(() => {
  role.admin = true;
});

describe("Demo 按钮状态", () => {
  it("加入前后保留同一展示集图标", () => {
    const inactive = mainIcon(render(false));
    const active = mainIcon(render(true));
    expect(active).toBe(inactive);
    expect(active).toContain("lucide-gallery-vertical-end");
  });

  it("已加入状态由角标与按下语义表达，不在首帧播放动效", () => {
    for (const active of [false, true]) {
      const html = render(active);
      expect(html).toContain(`aria-pressed="${active}"`);
      expect(html).toContain('data-slot="session-action-status"');
      expect(html).toContain(`data-active="${active}"`);
      expect(html).not.toContain('data-animate="true"');
    }
  });

  it("请求进行中禁用操作，但保留已有状态", () => {
    const html = render(true, true);
    expect(html).toMatch(/<button[^>]*\bdisabled=""/);
    expect(html).toContain('data-active="true"');
    expect(mainIcon(html)).toContain("lucide-gallery-vertical-end");
  });

  it("非管理员或 Demo 登录关闭时不提供写操作", () => {
    role.admin = false;
    expect(render(false)).toBe("");
    role.admin = true;
    expect(render(false, false, false)).toBe("");
  });
});
