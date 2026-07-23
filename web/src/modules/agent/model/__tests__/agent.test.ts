import { describe, it, expect } from 'vitest';

import { agentSchema, chatMessageSchema } from '../agent';

const baseAgent = {
  id: 'a1',
  name: 'Test',
  llmModel: 'gpt-4',
};

describe('agentSchema', () => {
  it('解析后端 camelCase 完整响应', () => {
    const parsed = agentSchema.parse({
      ...baseAgent,
      description: 'desc',
      type: 'react',
      systemPrompt: 'sp',
      maxIterations: 25,
      maxContextTokens: 8000,
      allowedSkills: ['s1'],
      mcpToolIds: ['m1'],
      knowledgeWorkspaceIds: ['k1'],
    });
    expect(parsed.allowedSkills).toEqual(['s1']);
    expect(parsed.mcpToolIds).toEqual(['m1']);
    expect(parsed.knowledgeWorkspaceIds).toEqual(['k1']);
  });

  it('数组字段为 null 时兜底为空数组（后端 nil slice 序列化场景）', () => {
    const parsed = agentSchema.parse({
      ...baseAgent,
      allowedSkills: null,
      mcpToolIds: null,
      knowledgeWorkspaceIds: null,
    });
    expect(parsed.allowedSkills).toEqual([]);
    expect(parsed.mcpToolIds).toEqual([]);
    expect(parsed.knowledgeWorkspaceIds).toEqual([]);
  });

  it('数组字段缺失时兜底为空数组', () => {
    const parsed = agentSchema.parse(baseAgent);
    expect(parsed.allowedSkills).toEqual([]);
    expect(parsed.mcpToolIds).toEqual([]);
    expect(parsed.knowledgeWorkspaceIds).toEqual([]);
  });

  it('字符串字段缺失填默认值', () => {
    const parsed = agentSchema.parse(baseAgent);
    expect(parsed.description).toBe('');
    expect(parsed.type).toBe('react');
    expect(parsed.systemPrompt).toBe('');
  });

  it('id 缺失抛出错误', () => {
    expect(() => agentSchema.parse({ name: 'x', llmModel: 'gpt' })).toThrow();
  });

  it('passthrough 保留未声明字段', () => {
    const parsed = agentSchema.parse({ ...baseAgent, embedModel: 'text-embedding-3' });
    expect((parsed as { embedModel?: string }).embedModel).toBe('text-embedding-3');
  });

  it('为历史 Agent 响应补齐系统管理字段', () => {
    const parsed = agentSchema.parse(baseAgent);
    expect(parsed.isSystem).toBe(false);
    expect(parsed.managementMode).toBe('');
  });

  it('将 artifact 及诊断报告内的 nullish 数组统一解析为空数组', () => {
    const parsed = chatMessageSchema.parse({
      role: 'assistant',
      artifacts: [{
        type: 'diagnostic_report',
        profileVersion: '2026-07-23.v1',
        citations: null,
        diagnosticReport: {
          facts: null,
          inferences: undefined,
          evidenceGaps: null,
          recommendedActions: undefined,
          citations: null,
          steps: undefined,
        },
      }],
    });

    expect(parsed.artifacts).toHaveLength(1);
    expect(parsed.artifacts[0]?.citations).toEqual([]);
    expect(parsed.artifacts[0]?.diagnosticReport).toEqual({
      facts: [],
      inferences: [],
      evidenceGaps: [],
      recommendedActions: [],
      citations: [],
      steps: [],
    });
  });

  it('拒绝 artifact 中类型错误的结构化证据', () => {
    expect(() => chatMessageSchema.parse({
      role: 'assistant',
      artifacts: [{ type: 'diagnostic_report', diagnosticReport: { facts: 'not-an-array' } }],
    })).toThrow();
  });
});
