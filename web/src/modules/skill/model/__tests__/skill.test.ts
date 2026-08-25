import { describe, expect, it } from 'vitest';

import { buildCreateSkillDraftPayload, skillWorkspaceSchema, type SkillFormValues } from '../skill';

describe('instruction Skill model', () => {
  it('创建 payload 只包含简化后的 name/description/instructions 三字段', () => {
    const values: SkillFormValues = {
      name: '投诉分类', description: '判断客户投诉类型', instructions: '先识别主题，再解释判断。',
    };
    expect(buildCreateSkillDraftPayload(values)).toEqual({
      name: '投诉分类', description: '判断客户投诉类型', instructions: '先识别主题，再解释判断。',
      editors: [],
    });
  });

  it('解析简化后的 revision 内容快照', () => {
    const workspace = skillWorkspaceSchema.parse({
      skill: { id: 'skill-1', name: '投诉分类', status: 'draft', draftRevisionId: 'draft-1' },
      draft: {
        id: 'draft-1', skillId: 'skill-1', status: 'draft',
        name: '投诉分类', description: '判断客户投诉类型', instructions: '遵循分类规则',
      },
    });
    expect(workspace.skill.draftRevisionId).toBe('draft-1');
    expect(workspace.draft.name).toBe('投诉分类');
    expect(workspace.draft.description).toBe('判断客户投诉类型');
    expect(workspace.draft.instructions).toBe('遵循分类规则');
  });
});
