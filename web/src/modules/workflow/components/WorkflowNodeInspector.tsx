import { Form, Input, Select, Typography } from 'antd';

import { parseMappingText } from '../model/editor';
import type { WorkflowNode } from '../model/workflow';

import { WorkflowAdvancedSettings } from './WorkflowAdvancedSettings';

const { Text } = Typography;
type Option = { value: string; label: string };

/** 映射字段是 schema 外展平文本：解析成功写回结构化 mapping 并删脏字段，失败保留上次合法值。 */
const applyMappingField = <K extends 'input_mapping' | 'output_mapping'>(
  node: WorkflowNode & Record<string, unknown>,
  values: Record<string, unknown>,
  key: K,
  textField: 'input_mapping_text' | 'output_mapping_text',
): WorkflowNode => {
  const text = values[textField];
  if (typeof text !== 'string') return node;
  // 在传入 node 上删脏字段：node 是 { ...node, ...values } 的合并结果，
  // 若用原始 values 重建会让前一个字段的脏键复活。
  const next = { ...node };
  delete next[textField];
  const mapping = parseMappingText(text, node[key]);
  if (mapping) next[key] = mapping;
  return next as WorkflowNode;
};

export const WorkflowNodeInspector = ({
  node,
  onChange,
  agents,
  skills,
  skillRevisions,
  mcpServers,
}: {
  node: WorkflowNode;
  onChange: (node: WorkflowNode) => void;
  agents: Option[];
  skills: Option[];
  skillRevisions: Option[];
  mcpServers: Option[];
}) => {
  const initialValues = {
    ...node,
    input_mapping_text: JSON.stringify(node.input_mapping || {}, null, 2),
    output_mapping_text: JSON.stringify(node.output_mapping || {}, null, 2),
  };
  return <aside className="workflow-node-inspector" aria-label="节点配置">
    <Text strong>节点配置</Text>
    <Form
      key={node.id}
      layout="vertical"
      initialValues={initialValues}
      onValuesChange={(_, values) => {
        let next = { ...node, ...values } as WorkflowNode & Record<string, unknown>;
        next = applyMappingField(next, values, 'input_mapping', 'input_mapping_text');
        next = applyMappingField(next, values, 'output_mapping', 'output_mapping_text');
        onChange(next);
      }}
    >
      <Form.Item label="节点名称" name="name"><Input aria-label="节点名称" placeholder="给这个步骤命名" /></Form.Item>
      {(node.type === 'agent' || node.type === 'skill') && <Form.Item label="执行 Agent" name="agent_id"><Select options={agents} /></Form.Item>}
      {node.type === 'skill' && <>
        <Form.Item label="Skill" name="skill_id"><Select options={skills} /></Form.Item>
        <Form.Item label="固定 Skill 修订" name="skill_revision_id"><Select options={skillRevisions} /></Form.Item>
      </>}
      {node.type === 'mcp_tool' && <>
        <Form.Item label="MCP 服务" name="mcp_server_id"><Select options={mcpServers} /></Form.Item>
        <Form.Item label="工具名称" name="mcp_tool_name"><Input /></Form.Item>
        <Form.Item label="副作用级别" name="effect_class"><Select options={[
          { value: 'pure', label: '纯读取' }, { value: 'idempotent', label: '幂等写入' }, { value: 'non_idempotent', label: '非幂等写入' },
        ]} /></Form.Item>
      </>}
      {node.type === 'condition' && <Form.Item label="判断表达式" name="condition"><Input.TextArea rows={3} /></Form.Item>}
      {node.type === 'approval' && <Text type="secondary">运行到这里时，将等待管理员审批后继续。</Text>}
      <WorkflowAdvancedSettings />
    </Form>
  </aside>;
};
