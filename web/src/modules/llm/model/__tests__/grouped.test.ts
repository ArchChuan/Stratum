import { describe, expect, it } from 'vitest';

import { buildGroupedModels } from '../grouped';

describe('buildGroupedModels', () => {
  it('透传模型的 contextWindow 与 maxTokens（vendor maxOut）用于默认值展示', () => {
    const grouped = buildGroupedModels(
      [{ providerId: 'p1', name: 'glm-5.2', capabilities: ['chat'], contextWindow: 128000, maxTokens: 8192 }],
      [{ id: 'p1', name: '托管厂商' }],
    );
    expect(grouped).toEqual([
      { provider: '托管厂商', models: [{ value: 'glm-5.2', label: 'glm-5.2', capabilities: ['chat'], reasoning: false, contextWindow: 128000, maxTokens: 8192 }] },
    ]);
  });

  it('透传能力标签并回退空数组（供模型选择器展示能力）', () => {
    const grouped = buildGroupedModels(
      [
        { providerId: 'p1', name: 'glm-5.2', capabilities: ['chat', 'tool_use'] },
        { providerId: 'p1', name: 'legacy-model', displayName: '旧模型' },
      ],
      [{ id: 'p1', name: '托管厂商' }],
    );
    expect(grouped[0]?.models).toEqual([
      { value: 'glm-5.2', label: 'glm-5.2', capabilities: ['chat', 'tool_use'], reasoning: false },
      { value: 'legacy-model', label: '旧模型', capabilities: [], reasoning: false },
    ]);
  });

  it('缺 maxTokens / contextWindow 时选项不带该字段（回落平台默认文案）', () => {
    const grouped = buildGroupedModels(
      [{ providerId: 'p1', name: 'legacy-model', displayName: '旧模型', capabilities: ['chat'] }],
      [{ id: 'p1', name: '托管厂商' }],
    );
    expect(grouped[0]?.models[0]).toEqual({ value: 'legacy-model', label: '旧模型', capabilities: ['chat'], reasoning: false });
  });

  it('透传 enabled 开关供选择器禁用停用模型（显示但不可选）', () => {
    const grouped = buildGroupedModels(
      [
        { providerId: 'p1', name: 'glm-5.2', capabilities: ['chat'], enabled: true },
        { providerId: 'p1', name: 'legacy-model', capabilities: ['chat'], enabled: false },
      ],
      [{ id: 'p1', name: '托管厂商' }],
    );
    expect(grouped[0]?.models).toEqual([
      { value: 'glm-5.2', label: 'glm-5.2', capabilities: ['chat'], reasoning: false, enabled: true },
      { value: 'legacy-model', label: 'legacy-model', capabilities: ['chat'], reasoning: false, enabled: false },
    ]);
  });

  it('未知厂商回退 providerId 作为分组名', () => {
    const grouped = buildGroupedModels(
      [{ providerId: 'unregistered', name: 'm1', capabilities: ['chat'] }],
      [],
    );
    expect(grouped).toEqual([
      { provider: 'unregistered', models: [{ value: 'm1', label: 'm1', capabilities: ['chat'], reasoning: false }] },
    ]);
  });
});
