// 执行状态展示映射与耗时格式化，表格与详情 Drawer 共用。

export const statusColors: Record<string, string> = { success: 'success', error: 'error' };
export const statusLabels: Record<string, string> = { success: '成功', error: '失败' };

export const formatDuration = (ms?: number) => {
  if (!ms) return '-';
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
};
