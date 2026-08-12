// 审批展示文案映射：状态/审批类型/风险等级 → 中文标签与 Tag 颜色。
// 与后端 domain 状态机（pending/approved/.../voided/invalidated）与 SubjectKind 枚举对齐。

export const APPROVAL_STATUS_LABELS: Record<string, string> = {
  pending: '待审批',
  approved: '已批准',
  rejected: '已拒绝',
  expired: '已过期',
  executing: '执行中',
  executed: '已执行',
  unknown_outcome: '结果未知',
  cancelled: '已取消',
  voided: '已失效',
  invalidated: '已失效',
  authorization_denied: '已阻止',
};

export const APPROVAL_STATUS_COLORS: Record<string, string> = {
  pending: 'gold',
  approved: 'green',
  rejected: 'red',
  expired: 'default',
  executing: 'blue',
  executed: 'green',
  unknown_outcome: 'orange',
  cancelled: 'default',
  voided: 'default',
  invalidated: 'default',
  authorization_denied: 'red',
};

export const SUBJECT_KIND_LABELS: Record<string, string> = {
  mcp_tool: 'MCP 工具',
  evaluation_action: '评测动作',
  mcp_policy: 'MCP 策略',
  mcp_server: 'MCP 服务器',
};

export const RISK_LEVEL_LABELS: Record<string, string> = {
  read: '只读',
  write_reversible: '可逆写入',
  destructive: '破坏性',
  unclassified: '未分类',
};

export const statusLabel = (status: string): string => APPROVAL_STATUS_LABELS[status] ?? status;
export const subjectKindLabel = (kind: string): string => SUBJECT_KIND_LABELS[kind] ?? kind;
export const riskLevelLabel = (level: string): string => RISK_LEVEL_LABELS[level] ?? level;
