import { describe, expect, it } from 'vitest';

import { buildCreateSkillPayload, skillWorkspaceSchema, type SkillFormValues } from '../skill';

describe('instruction Skill model', () => {
  it('创建 payload 只包含简化后的 name/description/instructions 三字段', () => {
    const values: SkillFormValues = {
      name: '投诉分类', description: '判断客户投诉类型', instructions: '先识别主题，再解释判断。',
    };
    expect(buildCreateSkillPayload(values)).toEqual({
      name: '投诉分类', description: '判断客户投诉类型', instructions: '先识别主题，再解释判断。',
      editors: [],
    });
  });

  it('解析版本化 workspace：active 为当前生效版本，含版本元数据', () => {
    const workspace = skillWorkspaceSchema.parse({
      skill: { id: 'skill-1', name: '投诉分类', status: 'published', activeRevisionId: 'rev-2' },
      active: {
        id: 'rev-2', skillId: 'skill-1', status: 'published', revisionNo: 2,
        name: '投诉分类', description: '判断客户投诉类型', instructions: '遵循分类规则',
        isCurrent: true, createdBy: 'user-1', createdAt: '2026-01-01T00:00:00Z',
      },
    });
    expect(workspace.skill.activeRevisionId).toBe('rev-2');
    expect(workspace.active.name).toBe('投诉分类');
    expect(workspace.active.description).toBe('判断客户投诉类型');
    expect(workspace.active.instructions).toBe('遵循分类规则');
    expect(workspace.active.isCurrent).toBe(true);
  });
});
