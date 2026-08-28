import { Button, Form, Input, Select, Typography } from 'antd';
import { useMemo } from 'react';

import type { WorkflowNode } from '../model/workflow';

import { ParameterMappingEditor } from './ParameterMappingEditor';

const { Text } = Typography;
type Option = { value: string; label: string };

export const WorkflowNodeInspector = ({
  node,
  onChange,
  onDelete,
  agents,
  skills,
  skillRevisions,
  mcpServers,
  upstreams,
  agentAllowedSkills,
}: {
  node: WorkflowNode;
  onChange: (node: WorkflowNode) => void;
  onDelete: (nodeId: string) => void;
  agents: Option[];
  skills: Option[];
  skillRevisions: Option[];
  mcpServers: Option[];
  upstreams: WorkflowNode[];
  agentAllowedSkills: Record<string, string[]>;
}) => {
  // 判别 union 安全取值：skill/agent 必有 agent_id，skill 必有 skill_id。
  const agentId = node.type === 'skill' || node.type === 'agent' ? node.agent_id : '';
  const skillId = node.type === 'skill' ? node.skill_id : '';
  // skill-agent 双向联动：选 agent 过滤 skills（空 allowedSkills = 无技能 → 空列表）；
  // 选 skill 反查支持它的 agent。未选对应端时展示全部，避免首轮选择被提前过滤。
  const skillOptions = useMemo(() => {
    if (!agentId) return skills;
    const allowed = agentAllowedSkills[agentId] || [];
    return skills.filter((skill) => allowed.includes(skill.value));
  }, [agentAllowedSkills, agentId, skills]);
  const agentOptions = useMemo(() => {
    if (!skillId) return agents;
    return agents.filter((agent) => (agentAllowedSkills[agent.value] || []).includes(skillId));
  }, [agentAllowedSkills, agents, skillId]);

  return <aside className="workflow-node-inspector" aria-label="节点配置">
    <Text strong>节点配置</Text>
    <Form
      key={node.id}
      layout="vertical"
      initialValues={{ ...node }}
      onValuesChange={(_, values) => {
        let next = { ...node, ...values } as WorkflowNode;
        // agent 变更后当前 skill_id 不在新 allowedSkills → 重置 skill，防残留非法绑定被后端保存拒绝。
        if (typeof values.agent_id === 'string' && values.agent_id !== agentId && next.type === 'skill') {
          const allowed = agentAllowedSkills[values.agent_id] || [];
          if (next.skill_id && !allowed.includes(next.skill_id)) {
            next = { ...next, skill_id: '', skill_revision_id: '' };
          }
        }
        // skill 变更后清空固定修订（新 skill 的 revision id 不复用旧 skill）。
        if (typeof values.skill_id === 'string' && values.skill_id !== skillId && next.type === 'skill') {
          next = { ...next, skill_revision_id: '' };
        }
        onChange(next);
      }}
    >
      <Form.Item label="节点名称" name="name"><Input aria-label="节点名称" placeholder="给这个步骤命名" /></Form.Item>
      {(node.type === 'agent' || node.type === 'skill') && <Form.Item label="执行 Agent" name="agent_id"><Select options={agentOptions} /></Form.Item>}
      {node.type === 'skill' && <>
        <Form.Item label="Skill" name="skill_id"><Select options={skillOptions} /></Form.Item>
        <Form.Item label="Skill 版本" name="skill_revision_id"><Select options={skillRevisions} /></Form.Item>
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
    </Form>
    <div className="workflow-param-panels">
      <ParameterMappingEditor
        key={`${node.id}-input`}
        direction="input"
        mapping={node.input_mapping || {}}
        upstreams={upstreams}
        onChange={(inputMapping) => onChange({ ...node, input_mapping: inputMapping })}
      />
      <ParameterMappingEditor
        key={`${node.id}-output`}
        direction="output"
        mapping={node.output_mapping || {}}
        upstreams={upstreams}
        onChange={(outputMapping) => onChange({ ...node, output_mapping: outputMapping })}
      />
    </div>
    <Button danger block onClick={() => onDelete(node.id)}>删除节点</Button>
  </aside>;
};
