import { Tag, Typography } from 'antd';

const LABELS: Record<string, string> = {
  skill: '技能', agent: 'Agent', mcp: 'MCP', knowledge: '知识库', active: '进行中', proposed: '待决定', promoted: '已晋级',
  succeeded: '已通过', failed: '未通过', paused: '已暂停', rejected: '已拒绝', running: '执行中',
  queued: '排队中', passed: '通过', pending: '等待数据', not_applicable: '不适用', cancelled: '已取消',
  draft: '草稿', published: '已发布', exact: '精确匹配', contains: '包含匹配', regex: '正则匹配', judge: 'AI 判定',
};

export const displayLabel = (value?: string) => LABELS[value || ''] || value || '未知';

export const runDisplayStatus = (status: string, passed: boolean) => status === 'succeeded'
  ? (passed ? 'passed' : 'failed') : status;

export const StatusTag = ({ value }: { value?: string }) => {
  const color = value === 'succeeded' || value === 'passed' ? 'success'
    : value === 'failed' || value === 'rejected' ? 'error'
      : value === 'pending' || value === 'queued' ? 'warning' : 'processing';
  return <Tag color={color}>{displayLabel(value)}</Tag>;
};

export const SafeValue = ({ value }: { value: unknown }) => (
  <Typography.Text code>{typeof value === 'string' ? value : JSON.stringify(value) ?? '—'}</Typography.Text>
);

export const drawerWidth = (isMobile?: boolean) => (isMobile ? '100%' : 720);
