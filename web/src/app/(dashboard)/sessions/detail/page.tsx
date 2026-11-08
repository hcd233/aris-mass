"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";
import SessionDetailClient from "@/components/session-detail/session-detail-client";
import { PermissionGuard } from "@/components/permission-guard";

function SessionDetailContent() {
  const searchParams = useSearchParams();
  const sessionId = Number(searchParams.get("id"));
  // key 绑定 sessionId：路由 query 变化不会重挂载组件，消息列表因此不会自动 reset，
  // 会出现「头部新会话、正文旧消息」。用 key 强制按会话重建组件生命周期。
  return <SessionDetailClient key={sessionId} sessionId={sessionId} />;
}

export default function SessionDetailPage() {
  return (
    <PermissionGuard module="sessions">
      <Suspense fallback={null}>
        <SessionDetailContent />
      </Suspense>
    </PermissionGuard>
  );
}
