import { describe, expect, it } from 'vitest';

import {
  validationIssueSchema,
  workflowDefinitionSchema,
  workflowInputSchemaSchema,
  workflowNodeSchema,
  workflowPageSchema,
  workflowVersionPageSchema,
} from './workflow';

const spec = {
  nodes: [
    { id: 'agent', type: 'agent', agent_id: 'agent-1' },
    { id: 'skill', type: 'skill', agent_id: 'agent-1', skill_id: 'skill-1', skill_revision_id: 'rev-1' },
    { id: 'tool', type: 'mcp_tool', mcp_server_id: 'server-1', mcp_tool_name: 'search', effect_class: 'pure' },
    { id: 'condition', type: 'condition', condition: 'input.approved == true' },
    { id: 'approval', type: 'approval' },
  ],
  edges: [],
  max_concurrency: 2,
};

describe('workflow wire models', () => {
  it('parses all five supported node types and rejects unknown types', () => {
    for (const node of spec.nodes) {
      expect(workflowNodeSchema.parse(node).type).toBe(node.type);
    }
    expect(() => workflowNodeSchema.parse({ id: 'n1', type: 'unknown' })).toThrow();
  });

  it('parses the seven versioned input field types', () => {
    const schema = workflowInputSchemaSchema.parse({
      task_label: '任务',
      task_description: '说明本次要完成的工作',
      fields: [
        { key: 'short', label: '短文本', type: 'short_text', required: true },
        { key: 'long', label: '长文本', type: 'long_text' },
        { key: 'number', label: '数字', type: 'number', default: 1 },
        { key: 'single', label: '单选', type: 'single_select', options: [{ label: '甲', value: 'a' }] },
        { key: 'multi', label: '多选', type: 'multi_select', options: [{ label: '乙', value: 'b' }] },
        { key: 'enabled', label: '布尔', type: 'boolean', default: true },
        { key: 'date', label: '日期', type: 'date' },
      ],
    });
    expect(schema.fields).toHaveLength(7);
  });

  it('parses definitions, catalog pages, version pages, and validation issues', () => {
    const definition = workflowDefinitionSchema.parse({
      id: 'workflow-1',
      name: '客户研究',
      description: '形成研究摘要',
      revision: 3,
      spec,
      input_schema: { task_label: '任务', fields: [] },
      created_at: '2026-07-23T00:00:00Z',
      updated_at: '2026-07-23T01:00:00Z',
    });
    expect(definition.spec.nodes).toHaveLength(5);

    expect(workflowPageSchema.parse({
      workflows: [{ id: 'workflow-1', name: '客户研究', description: '', revision: 3, updated_at: '2026-07-23T01:00:00Z' }],
      total: 1,
      page: 1,
      page_size: 20,
    }).total).toBe(1);

    expect(workflowVersionPageSchema.parse({
      versions: [{ id: 'version-1', definition_id: 'workflow-1', version: 2, name: '客户研究', description: '', created_at: '2026-07-23T02:00:00Z' }],
      total: 1,
      page: 1,
      page_size: 20,
    }).versions[0].version).toBe(2);

    expect(validationIssueSchema.parse({ node_id: 'agent', code: 'invalid', message: '请选择 Agent' }).node_id).toBe('agent');
  });

  it('rejects malformed API payloads instead of silently accepting them', () => {
    expect(() => workflowPageSchema.parse({ workflows: [{ id: 42 }], total: 1, page: 1, page_size: 20 })).toThrow();
  });
});
