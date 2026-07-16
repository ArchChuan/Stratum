import { describe, expect, it } from 'vitest';

import {
  buildCreateSkillPayload,
  buildCreateSkillDraftPayload,
  buildDraftSkillTestPayload,
  skillWorkspaceSchema,
  parseSkillTestInput,
  type SkillFormValues,
} from '../skill';

describe('SkillFormValues', () => {
  it('统一创建表单不要求用户选择内部 skill 类型', () => {
    const values: SkillFormValues = {
      name: '投诉分类',
      description: '将客户投诉分成物流、质量、售后和价格四类，并说明理由。',
    };

    expect(values.name).toBe('投诉分类');
    expect(values.type).toBeUndefined();
  });

  it('统一创建 payload 不发送内部 skill 类型', () => {
    const payload = buildCreateSkillPayload({
      name: '投诉分类',
      description: '将客户投诉分成物流、质量、售后和价格四类，并说明理由。',
    });

    expect(payload).toEqual({
      name: '投诉分类',
      description: '将客户投诉分成物流、质量、售后和价格四类，并说明理由。',
    });
    expect(payload.type).toBeUndefined();
  });

  it('旧 HTTP runtime payload 保留 headers JSON 解析结果', () => {
    const payload = buildCreateSkillPayload({
      name: '外部查询',
      type: 'http',
      url: 'https://api.example.com/search',
      method: 'POST',
      headersJson: '{"X-Token":"abc"}',
    });

    expect(payload.headers).toEqual({ 'X-Token': 'abc' });
    expect(payload.headersJson).toBeUndefined();
  });

  it('测试输入支持 JSON 对象', () => {
    expect(parseSkillTestInput('{"text":"客户投诉"}')).toEqual({ text: '客户投诉' });
  });

  it('测试输入不是 JSON 时按文本发送', () => {
    expect(parseSkillTestInput('客户投诉')).toBe('客户投诉');
  });

  it('草稿测试 payload 包含当前技能草稿和测试输入', () => {
    expect(
      buildDraftSkillTestPayload(
        {
          name: '投诉分类',
          description: '将客户投诉分类',
          expectedOutput: '分类和理由',
        },
        '{"text":"客户反馈快递三天没有更新"}',
      ),
    ).toEqual({
      skill: {
        name: '投诉分类',
        description: '将客户投诉分类',
        expectedOutput: '分类和理由',
      },
      input: { text: '客户反馈快递三天没有更新' },
    });
  });

  it('最小能力创建 payload 只包含能力定义字段', () => {
    expect(
      buildCreateSkillDraftPayload({
        name: '投诉分类',
        goal: '判断客户投诉类型',
        whenToUse: '用户表达投诉时',
        sampleInput: '快递没更新',
        expectedOutput: '物流问题',
      }),
    ).toEqual({
      name: '投诉分类',
      goal: '判断客户投诉类型',
      whenToUse: '用户表达投诉时',
      sampleInput: '快递没更新',
      expectedOutput: '物流问题',
    });
  });

  it('Skill 工作台响应包含 skill 与 draft version', () => {
    const workspace = skillWorkspaceSchema.parse({
      skill: {
        id: 'skill-1',
        name: '投诉分类',
        description: '判断客户投诉类型',
        status: 'draft',
        draftVersionId: 'draft-1',
      },
      draft: {
        id: 'draft-1',
        skillId: 'skill-1',
        status: 'draft',
        capability: { goal: '判断客户投诉类型' },
        toolContract: { toolName: 'classify_complaint' },
        implementation: { mode: 'prompt' },
      },
    });

    expect(workspace.skill.id).toBe('skill-1');
    expect(workspace.draft.status).toBe('draft');
  });
});
