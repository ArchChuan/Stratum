import { Input, Segmented, Select, Space } from 'antd';

import type { WorkflowRunStatus } from '../model/workflow';

const statuses = [
  { value: '', label: '全部状态' },
  { value: 'queued', label: '排队中' },
  { value: 'running', label: '运行中' },
  { value: 'completed', label: '已完成' },
  { value: 'failed', label: '失败' },
  { value: 'paused', label: '已暂停' },
  { value: 'canceled', label: '已取消' },
  { value: 'manual_intervention', label: '等待人工处理' },
];

export const WorkflowRunFilters = ({
  isAdmin,
  definitionId,
  status,
  onDefinitionChange,
  onStatusChange,
}: {
  isAdmin: boolean;
  definitionId: string;
  status: WorkflowRunStatus | '';
  onDefinitionChange: (value: string) => void;
  onStatusChange: (value: WorkflowRunStatus | '') => void;
}) => <Space wrap className="workflow-run-filters">
  {isAdmin && <Segmented aria-label="运行范围" options={['我的运行', '全部运行']} defaultValue="全部运行" />}
  <Input allowClear aria-label="工作流标识" placeholder="按工作流标识筛选" value={definitionId} onChange={(event) => onDefinitionChange(event.target.value)} />
  <Select aria-label="运行状态" value={status} options={statuses} onChange={onStatusChange} style={{ width: 160 }} />
</Space>;
