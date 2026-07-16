import { describe, expect, it } from 'vitest';

import { buildCreateSkillDraftPayload, skillWorkspaceSchema, type SkillFormValues } from '../skill';

describe('instruction Skill model', () => {
  it('创建 payload 只包含指令包字段', () => {
    const values: SkillFormValues = {
      name: '投诉分类', goal: '判断客户投诉类型', whenToUse: '用户表达投诉时',
      sampleInput: '快递没更新', expectedOutput: '物流问题', instructions: '先识别主题，再解释判断。',
      mcpToolIDs: 'mcp:orders:get\nmcp:crm:lookup', knowledgeWorkspaceIDs: 'kb-1',
      memoryScopes: ['conversation'],
    };
    expect(buildCreateSkillDraftPayload(values)).toEqual({
      name: '投诉分类', goal: '判断客户投诉类型', whenToUse: '用户表达投诉时',
      sampleInput: '快递没更新', expectedOutput: '物流问题', instructions: '先识别主题，再解释判断。',
      requirements: {
        mcpToolIds: ['mcp:orders:get', 'mcp:crm:lookup'],
        knowledgeWorkspaceIds: ['kb-1'], memoryScopes: ['conversation'],
      },
    });
  });

  it('解析 revision instruction bundle', () => {
    const workspace = skillWorkspaceSchema.parse({
      skill: { id: 'skill-1', name: '投诉分类', status: 'draft', draftRevisionId: 'draft-1' },
      draft: {
        id: 'draft-1', skillId: 'skill-1', status: 'draft',
        capability: { goal: '判断客户投诉类型' },
        activationContract: { name: 'classify_complaint', confirmed: false },
        instructions: '遵循分类规则',
        requirements: { mcpToolIds: ['mcp:orders:get'], memoryScopes: ['conversation'] },
      },
    });
    expect(workspace.skill.draftRevisionId).toBe('draft-1');
    expect(workspace.draft.instructions).toBe('遵循分类规则');
    expect(workspace.draft.requirements.mcpToolIds).toEqual(['mcp:orders:get']);
  });
});
