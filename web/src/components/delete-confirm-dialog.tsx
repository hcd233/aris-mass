"use client";

import { AlertTriangle } from "lucide-react";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";

interface DeleteConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 对话框标题（调用方本地化） */
  title: string;
  /** 警示描述文本 */
  description: string;
  /** 确认按钮文字，如「删除」「撤销」 */
  confirmLabel: string;
  /** 执行中按钮文字；缺省时执行中也显示 confirmLabel */
  loadingLabel?: string;
  /** 异步执行进行中：禁用确认按钮并切换文字 */
  loading?: boolean;
  onConfirm: () => void;
}

/** 全站统一的删除/撤销二次确认对话框。 */
export function DeleteConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  loadingLabel,
  loading = false,
  onConfirm,
}: DeleteConfirmDialogProps) {
  const t = useT();
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        // 执行中不接受 Esc / 遮罩 / 取消关闭：避免请求还在飞、弹窗已消失，
        // loading 文案与失败重试路径全部不可见。
        if (loading && !next) return;
        onOpenChange(next);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            <AlertTriangle className="size-5 text-destructive" />
            {title}
          </AlertDialogTitle>
          <AlertDialogDescription className="break-words">{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={loading}>{t("common.cancel")}</AlertDialogCancel>
          {/* 不用 AlertDialogAction：它本质是 Close，点击即关闭，loading 态永远不可见 */}
          <Button variant="destructive" onClick={onConfirm} disabled={loading}>
            {loading && loadingLabel ? loadingLabel : confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
