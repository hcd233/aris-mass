import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Share2 } from "lucide-react";
import { describe, expect, it } from "vitest";
import { SessionActionButton } from "../../../../components/session-action-button";

const source = readFileSync(new URL("../page.tsx", import.meta.url), "utf8");
const types = readFileSync(new URL("../../../../lib/types.ts", import.meta.url), "utf8");
const dialog = readFileSync(
  new URL("../../../../components/share/share-dialog.tsx", import.meta.url),
  "utf8",
);

describe("会话列表分享入口", () => {
  it("桌面与移动端复用分享弹窗，并阻止行点击跳转", () => {
    expect(source).toContain('import { ShareDialog } from "@/components/share/share-dialog"');
    expect(source.match(/openShare\(s\)/g)).toHaveLength(2);
    expect(source.match(/e\.stopPropagation\(\);\s*openShare\(s\);/g)).toHaveLength(2);
    expect(source).toContain("sessionId={shareSession.sessionId}");
  });

  it("列表数据直接提供分享 ID，打开时无需再请求详情", () => {
    const summary = types.slice(types.indexOf("interface SessionSummary"));
    expect(summary).toContain("shareID?: string");
    expect(source).toContain("existingShareID: s.shareID");
    expect(source).not.toContain("api.getSessionMetadata(s.id)");
  });

  it("桌面和移动端回显分享角标，主图标与管理文案保持一致", () => {
    expect(source.match(/active=\{!!s\.shareID\}/g)).toHaveLength(2);
    expect(
      source.match(/s\.shareID\s*\?\s*t\("session_detail\.manage_share_aria"\)/g),
    ).toHaveLength(2);
    const render = (active: boolean) =>
      renderToStaticMarkup(createElement(SessionActionButton, { active, icon: Share2 }));
    const inactive = render(false);
    const active = render(true);
    const mainIcon = (html: string) => html.match(/<svg\b[^>]*>[\s\S]*?<\/svg>/)?.[0];
    expect(mainIcon(active)).toBe(mainIcon(inactive));
    expect(mainIcon(active)).toContain("lucide-share");
    expect(active).toContain('data-slot="session-action-status"');
    expect(active).toContain('data-active="true"');
    expect(inactive).toContain('data-active="false"');
  });

  it("创建成功后立即更新对应列表行的分享状态", () => {
    expect(dialog).toContain("onShareCreated?: (shareID: string) => void");
    expect(dialog).toContain("onShareCreated?.(rsp.shareId)");
    expect(source).toContain("onShareCreated={handleShareCreated}");
    expect(source).toContain("s.id === sessionId ? { ...s, shareID } : s");
  });

  it("操作列能容纳 demo、分享与删除三个图标按钮", () => {
    const labelIndex = source.indexOf('t("common.actions")');
    const headerIndex = source.lastIndexOf('<TableHead className="w-[', labelIndex);
    const match = source.slice(headerIndex).match(/^<TableHead className="w-\[(\d+)px\]">/);
    expect(Number(match?.[1])).toBeGreaterThanOrEqual(140);
  });
});
