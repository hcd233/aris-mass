import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

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

  it("已分享会话在桌面与移动端均使用高亮和管理文案", () => {
    expect(source.match(/variant=\{s\.shareID \? "secondary" : "ghost"\}/g)).toHaveLength(2);
    expect(
      source.match(/s\.shareID\s*\?\s*t\("session_detail\.manage_share_aria"\)/g),
    ).toHaveLength(2);
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
