import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

/**
 * 为什么是文本断言：仓库没有 jsdom / testing-library，hook 里的点击与异步链路无法在 vitest 中渲染；
 * 沿用分享入口测试的做法，锁定两个已修复的回归点——按行记录在途状态、点击后先翻转再请求。
 */
const hook = readFileSync(
  new URL("../../../../hooks/use-demo-whitelist.ts", import.meta.url),
  "utf8",
);
const sessions = readFileSync(new URL("../page.tsx", import.meta.url), "utf8");
const detail = readFileSync(
  new URL("../../../../components/session-detail/session-detail-client.tsx", import.meta.url),
  "utf8",
);

describe("Demo 白名单切换", () => {
  it("在途状态按 session id 计算，一行请求中不影响其它行", () => {
    expect(sessions.match(/pending=\{isPending\(s\.id\)\}/g)).toHaveLength(2);
    expect(detail).toContain("pending={isPending(metadata.id)}");
    expect(sessions).not.toMatch(/pending=\{pending\}/);
    expect(detail).not.toMatch(/pending=\{pending\}/);
    expect(hook).toContain("if (pendingRef.current.has(id)) return");
    expect(hook).not.toContain("if (pendingRef.current) return");
  });

  it("点击后先翻转本地集合再发请求，失败时回滚", () => {
    const flip = hook.indexOf("setDemoIds((prev) => withMembership(prev, id, !wasInDemo))");
    const request = hook.indexOf("const request = wasInDemo");
    const rollback = hook.indexOf("withMembership(prev, id, wasInDemo)");
    expect(flip).toBeGreaterThan(-1);
    expect(request).toBeGreaterThan(flip);
    expect(rollback).toBeGreaterThan(request);
  });

  it("连接口返回按行的 isPending，不再暴露全局 pending", () => {
    expect(hook).toContain("const [pendingIds, setPendingIds] = useState<Set<number>>(new Set())");
    expect(hook).toContain("return { loginEnabled, isInDemo, isPending, toggle }");
    expect(hook).not.toContain("return { loginEnabled, pending, isInDemo, toggle }");
  });
});
