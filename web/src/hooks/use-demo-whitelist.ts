"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { api } from "@/lib/api-client";
import { showErrorToast } from "@/lib/api-error-handler";
import { useAuth } from "@/lib/auth-context";
import { useT } from "@/lib/i18n";

/** 与后端 DemoSessionMaxPageSize 一致（internal/common/constant/string.go） */
const DEMO_WHITELIST_PAGE_SIZE = 100;

/** 返回把 id 加入 / 移出后的新集合；用函数式 setState 承接并发的多行切换 */
function withMembership(ids: Set<number>, id: number, present: boolean) {
  const next = new Set(ids);
  if (present) next.add(id);
  else next.delete(id);
  return next;
}

/**
 * demo 白名单视角（admin 用）：loginEnabled + 全部白名单 ID 集合。
 * 挂载时拉取一次 getDemoConfig 与 listDemoSessions 全量；toggle(id) 单条添加/移除。
 * toggle 为乐观更新：点击立即翻转本地集合，写请求在后台完成，失败回滚并提示；
 * pending 按 session 维度记录，单行在途时只禁用该行按钮，不影响其它行。
 * 非 admin 一律 fail-closed：不拉取 admin-only 的 listDemoSessions，返回空集合与 no-op toggle。
 */
export function useDemoWhitelist() {
  const t = useT();
  const { isAdmin } = useAuth();
  const [loginEnabled, setLoginEnabled] = useState(false);
  const [demoIds, setDemoIds] = useState<Set<number>>(new Set());
  const [pendingIds, setPendingIds] = useState<Set<number>>(new Set());
  const pendingRef = useRef<Set<number>>(new Set());

  useEffect(() => {
    if (!isAdmin()) return; // 非 admin 不调用 admin-only 白名单接口（避免 403 噪音请求）
    let cancelled = false;
    (async () => {
      try {
        const cfg = await api.getDemoConfig();
        if (!cancelled) setLoginEnabled(cfg.config?.loginEnabled ?? false);
      } catch (err) {
        if (!cancelled) showErrorToast(err); // 失败保持 false → 按钮不渲染（fail-closed）
      }
      try {
        const ids = new Set<number>();
        let page = 1;
        for (;;) {
          const rsp = await api.listDemoSessions(page, DEMO_WHITELIST_PAGE_SIZE);
          if (cancelled) return;
          for (const s of rsp.sessions ?? []) ids.add(s.id);
          const total = Number(rsp.pageInfo?.total ?? ids.size);
          if (page * DEMO_WHITELIST_PAGE_SIZE >= total) break;
          page += 1;
        }
        if (!cancelled) setDemoIds(ids);
      } catch (err) {
        if (!cancelled) showErrorToast(err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [isAdmin]);

  const isInDemo = useCallback((id: number) => demoIds.has(id), [demoIds]);

  const isPending = useCallback((id: number) => pendingIds.has(id), [pendingIds]);

  const toggle = useCallback(
    (id: number) => {
      if (!isAdmin()) return; // 兜底：非 admin 不触发白名单写操作
      if (pendingRef.current.has(id)) return; // 同一行在途时忽略重复点击，避免来回翻转
      const wasInDemo = demoIds.has(id);

      pendingRef.current.add(id);
      setPendingIds((prev) => withMembership(prev, id, true));
      // 乐观翻转：按钮状态与勾选立即响应，不等后端往返
      setDemoIds((prev) => withMembership(prev, id, !wasInDemo));

      const request = wasInDemo
        ? api.removeDemoSessions([id])
        : api.addDemoSessions({ sessionIds: [id] });
      request
        .then(() => toast.success(wasInDemo ? t("demo.removed") : t("demo.added")))
        .catch((err) => {
          setDemoIds((prev) => withMembership(prev, id, wasInDemo)); // 失败回滚到点击前的状态
          showErrorToast(err);
        })
        .finally(() => {
          pendingRef.current.delete(id);
          setPendingIds((prev) => withMembership(prev, id, false));
        });
    },
    [demoIds, t, isAdmin],
  );

  return { loginEnabled, isInDemo, isPending, toggle };
}
