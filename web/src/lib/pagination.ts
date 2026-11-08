/**
 * 把页码钳制到 [1, totalPages]。
 *
 * 页码会持久化到 localStorage，数据被删（本人或他人）后可能超过总页数；
 * 不钳制会停在空页且分页条仍显示旧页码。total 为 0 时钳到第 1 页。
 */
export function clampPage(page: number, total: number, pageSize: number): number {
  const size = Math.max(1, Math.floor(pageSize) || 1);
  const totalPages = Math.max(1, Math.ceil(Math.max(0, total) / size));
  if (!Number.isFinite(page)) return 1;
  return Math.min(Math.max(1, Math.floor(page)), totalPages);
}
