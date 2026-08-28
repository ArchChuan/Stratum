import { Descriptions, Empty, Typography } from 'antd';

import type { WorkflowNode } from '../model/workflow';

const { Text } = Typography;

const TYPE_LABELS: Record<WorkflowNode['type'], string> = {
  agent: 'Agent',
  skill: 'Skill',
  mcp_tool: 'MCP 工具',
  condition: '条件判断',
  approval: '人工审批',
};

const EFFECT_LABELS: Record<string, string> = {
  pure: '纯读取',
  idempotent: '幂等写入',
  non_idempotent: '非幂等写入',
};

const mappingText = (mapping: Record<string, string>): string => {
  const entries = Object.entries(mapping ?? {});
  return entries.length ? entries.map(([key, value]) => `${key} → ${value}`).join('\n') : '—';
};

// WorkflowNodeReadonlyDetail 是工作流详情页/版本页共用的只读节点配置面板：
// 点击画布节点后展示该节点的执行对象、参数映射、重试与超时等配置，禁止编辑。
// skillRevisionLabels 把 skill_revision_id 翻译成可读的版本名（如「检索（已发布）」），
// 缺失时回退显示原始 revision ID。
export const WorkflowNodeReadonlyDetail = ({ node, skillRevisionLabels }: {
  node?: WorkflowNode;
  skillRevisionLabels?: Record<string, string>;
}) => {
  if (!node) return <Empty description="点击节点查看配置详情" />;
  return <aside aria-label="节点配置详情" className="workflow-node-readonly-detail">
    <Text strong>{node.name || TYPE_LABELS[node.type]}节点</Text>
    <Descriptions column={1} size="small">
      <Descriptions.Item label="类型">{TYPE_LABELS[node.type]}</Descriptions.Item>
      {node.type === 'agent' && <Descriptions.Item label="执行 Agent">{node.agent_id}</Descriptions.Item>}
      {node.type === 'skill' && <>
        <Descriptions.Item label="执行 Agent">{node.agent_id}</Descriptions.Item>
        <Descriptions.Item label="Skill">{node.skill_id}</Descriptions.Item>
        {node.skill_revision_id
          ? <Descriptions.Item label="Skill 版本">{skillRevisionLabels?.[node.skill_revision_id] ?? node.skill_revision_id}</Descriptions.Item>
          : null}
      </>}
      {node.type === 'mcp_tool' && <>
        <Descriptions.Item label="MCP 服务">{node.mcp_server_id}</Descriptions.Item>
        <Descriptions.Item label="工具名称">{node.mcp_tool_name}</Descriptions.Item>
        <Descriptions.Item label="副作用级别">{EFFECT_LABELS[node.effect_class] ?? node.effect_class}</Descriptions.Item>
      </>}
      {node.type === 'condition' && <Descriptions.Item label="判断表达式">{node.condition}</Descriptions.Item>}
      {node.type === 'approval' && <Descriptions.Item label="说明">运行到这里时，等待管理员审批后继续。</Descriptions.Item>}
      <Descriptions.Item label="超时">{node.timeout_ms ? `${node.timeout_ms}ms` : '不限制'}</Descriptions.Item>
      <Descriptions.Item label="重试">
        {node.retry?.max_attempts ? `最多 ${node.retry.max_attempts} 次${node.retry.backoff_ms ? `，间隔 ${node.retry.backoff_ms}ms` : ''}` : '不重试'}
      </Descriptions.Item>
      <Descriptions.Item label="输入映射"><Text style={{ whiteSpace: 'pre-wrap' }}>{mappingText(node.input_mapping)}</Text></Descriptions.Item>
      <Descriptions.Item label="输出映射"><Text style={{ whiteSpace: 'pre-wrap' }}>{mappingText(node.output_mapping)}</Text></Descriptions.Item>
    </Descriptions>
  </aside>;
};
