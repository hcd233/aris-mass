import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(new URL("../page.tsx", import.meta.url), "utf8");

describe("会话列表分享入口", () => {
  it("桌面与移动端复用分享弹窗，并阻止行点击跳转", () => {
    expect(source).toContain('import { ShareDialog } from "@/components/share/share-dialog"');
    expect(source.match(/void openShare\(s\)/g)).toHaveLength(2);
    expect(source.match(/e\.stopPropagation\(\);\s*void openShare\(s\);/g)).toHaveLength(2);
    expect(source).toContain("sessionId={shareSession.sessionId}");
  });

  it("打开前读取现有分享，避免已分享会话重复创建", () => {
    expect(source).toContain("api.getSessionMetadata(s.id)");
    expect(source).toContain("existingShareID: rsp.session?.shareID");
    expect(source).toContain("existingShareID={shareSession.existingShareID}");
  });

  it("操作列能容纳 demo、分享与删除三个图标按钮", () => {
    const labelIndex = source.indexOf('t("common.actions")');
    const headerIndex = source.lastIndexOf('<TableHead className="w-[', labelIndex);
    const match = source.slice(headerIndex).match(/^<TableHead className="w-\[(\d+)px\]">/);
    expect(Number(match?.[1])).toBeGreaterThanOrEqual(140);
  });
});
