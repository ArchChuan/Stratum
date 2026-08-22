import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { parametersApi } from '../../api/parameters.api';
import type { ParameterDefinition } from '../../model/parameters';
import { PlatformSettingsPage } from '../PlatformSettingsPage';

vi.mock('../../api/parameters.api', () => ({
  parametersApi: { schema: vi.fn(), list: vi.fn(), update: vi.fn() },
}));

vi.mock('@/modules/llm', () => ({
  llmApi: {
    listModels: vi.fn().mockResolvedValue([]),
    listProviders: vi.fn().mockResolvedValue([]),
  },
}));

// 平台 scope 定义：非 0 默认值由 List 回填，缺失键 = 0/''/nil 默认（0=unset）。
const defs = (): ParameterDefinition[] => [
  {
    key: 'memory.enrich_temperature',
    scope: 'platform',
    category: '记忆',
    display_name: '记忆丰富温度',
    value_type: 'float',
    default: 0.1,
    description: '',
    optimizable: false,
    sensitive: false,
    visual_hint: { control: 'slider', min: 0, max: 1, step: 0.05 },
  },
  {
    key: 'evaluation.judge.temperature',
    scope: 'platform',
    category: '评测',
    display_name: '评测温度',
    value_type: 'float',
    default: 0,
    description: '',
    optimizable: false,
    sensitive: false,
    visual_hint: { control: 'number', min: 0, max: 1, step: 0.1 },
  },
  {
    key: 'memory.supersede_prompt',
    scope: 'platform',
    category: '记忆',
    display_name: '记忆取代提示词',
    value_type: 'string',
    default: '',
    description: '',
    optimizable: false,
    sensitive: false,
    visual_hint: { control: 'textarea' },
  },
  {
    key: 'memory.enrich_prompt',
    scope: 'platform',
    category: '记忆',
    display_name: '记忆丰富提示词',
    value_type: 'string',
    default: '',
    description: '',
    optimizable: false,
    sensitive: false,
    visual_hint: { control: 'textarea' },
  },
];

const clickTab = (name: string) => {
  fireEvent.click(screen.getByRole('tab', { name }));
};

describe('PlatformSettingsPage', () => {
  it('groups platform params into domain tabs and populates loaded values', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    // 平台值必须回填到控件：这是"刷新后编辑参数变空"类回归的防线。
    vi.mocked(parametersApi.list).mockResolvedValue({
      'memory.enrich_prompt': '平台级富化提示词',
      'memory.supersede_prompt': '平台级取代提示词',
      'memory.enrich_temperature': 0.9,
    });

    render(<PlatformSettingsPage />);

    expect(await screen.findByRole('tab', { name: '记忆' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '评测' })).toBeInTheDocument();
    // 默认激活第一个领域 tab,加载值回填到控件。
    expect(await screen.findByDisplayValue('平台级富化提示词')).toBeInTheDocument();
    expect(screen.getByDisplayValue('平台级取代提示词')).toBeInTheDocument();
  });

  it('never renders resource defaults: resource-scope keys belong to the resource layer', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue([
      ...defs(),
      {
        key: 'agent.temperature',
        scope: 'resource',
        category: 'agent',
        display_name: 'Agent 温度',
        value_type: 'float',
        default: 0.7,
        description: '',
        optimizable: true,
        sensitive: false,
        visual_hint: { control: 'slider', min: 0, max: 1, step: 0.1 },
      },
      {
        key: 'memory.long_term_top_k',
        scope: 'resource',
        category: 'memory',
        display_name: '废弃记忆参数',
        value_type: 'int',
        default: 5,
        description: '',
        optimizable: false,
        sensitive: false,
        visual_hint: { control: 'slider', min: 1, max: 20, step: 1 },
      },
      {
        key: 'agent.bindings',
        scope: 'resource',
        category: 'agent',
        display_name: 'Agent 绑定',
        value_type: 'string',
        default: null,
        description: '',
        optimizable: false,
        sensitive: false,
        visual_hint: { control: 'textarea' },
      },
    ]);
    vi.mocked(parametersApi.list).mockResolvedValue({ 'agent.temperature': 0.3 });

    render(<PlatformSettingsPage />);

    expect(await screen.findByRole('tab', { name: '记忆' })).toBeInTheDocument();
    expect(screen.queryByText('资源默认值')).not.toBeInTheDocument();
    expect(screen.queryByText('Agent 温度')).not.toBeInTheDocument();
    expect(screen.queryByText('废弃记忆参数')).not.toBeInTheDocument();
    expect(screen.queryByText('Agent 绑定')).not.toBeInTheDocument();
    expect(screen.queryByText('会影响所有未单独配置的 Agent')).not.toBeInTheDocument();
  });

  it('shows per-tab default hints for missing keys and hides hints for set keys', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    // 非 0 默认键被后端回填(已设置),0 默认键缺失
    vi.mocked(parametersApi.list).mockResolvedValue({ 'memory.enrich_temperature': 0.9 });

    render(<PlatformSettingsPage />);

    expect(await screen.findByText('记忆丰富温度')).toBeInTheDocument();
    // List 回填后 hint 消失：antd Form setFieldsValue 的 watch 更新可能滞后
    // 于 schema 渲染一帧（慢机器上明显），同步 queryByText 会命中中间帧。
    await waitFor(() => expect(screen.queryByText('默认：0.1')).not.toBeInTheDocument());

    clickTab('评测');
    expect(screen.getByText('评测温度')).toBeInTheDocument();
    expect(screen.getByText('默认：0（未设置）')).toBeInTheDocument();
  });

  it('shows the non-zero default hint when the key is missing entirely', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({});

    render(<PlatformSettingsPage />);

    expect(await screen.findByText('默认：0.1')).toBeInTheDocument();
    clickTab('评测');
    expect(screen.getByText('默认：0（未设置）')).toBeInTheDocument();
  });

  it('shows 未设置（使用定义默认） for string keys with empty default and no prompt viewers', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({});

    render(<PlatformSettingsPage />);

    expect(await screen.findByText('记忆取代提示词')).toBeInTheDocument();
    expect(screen.getAllByText('未设置（使用定义默认）')).toHaveLength(2);
    // S2：memory.*_prompt 无内置模板，不再渲染"查看默认提示词"。
    expect(screen.queryByRole('button', { name: '查看默认提示词' })).not.toBeInTheDocument();
  });

  it('renders no hint for toggle keys', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue([
      {
        key: 'rag.enabled',
        scope: 'platform',
        category: '其他',
        display_name: 'RAG 开关',
        value_type: 'bool',
        default: false,
        description: '',
        optimizable: false,
        sensitive: false,
        visual_hint: { control: 'toggle' },
      },
    ]);
    vi.mocked(parametersApi.list).mockResolvedValue({});

    render(<PlatformSettingsPage />);

    expect(await screen.findByRole('tab', { name: '其他' })).toBeInTheDocument();
    expect(screen.getByText('RAG 开关')).toBeInTheDocument();
    // toggle 恒被 List 返回,无缺失场景,不渲染 hint(副标题含"未设置"文案,故精确匹配 hint 文本)
    expect(screen.queryByText('默认：', { exact: false })).not.toBeInTheDocument();
    expect(screen.queryByText('未设置（使用定义默认）')).not.toBeInTheDocument();
  });

  it('saves only the active tab keys (per-domain independent save)', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({
      'memory.enrich_temperature': 0.9,
      'evaluation.judge.temperature': 0.5,
    });
    vi.mocked(parametersApi.update).mockResolvedValue({});

    render(<PlatformSettingsPage />);
    await screen.findByText('记忆丰富温度');

    fireEvent.click(screen.getByRole('button', { name: '保存记忆参数' }));
    await waitFor(() =>
      expect(parametersApi.update).toHaveBeenLastCalledWith(
        expect.objectContaining({ 'memory.enrich_temperature': 0.9 }),
      ),
    );
    // 记忆 tab 保存只提交记忆领域 key,不包含评测 key。
    const memoryCalls = vi.mocked(parametersApi.update).mock.calls;
    const memoryCall = memoryCalls[memoryCalls.length - 1][0] as Record<string, unknown>;
    expect(Object.keys(memoryCall).every((k) => k.startsWith('memory.'))).toBe(true);

    clickTab('评测');
    fireEvent.click(screen.getByRole('button', { name: '保存评测参数' }));
    await waitFor(() =>
      expect(parametersApi.update).toHaveBeenLastCalledWith(
        expect.objectContaining({ 'evaluation.judge.temperature': 0.5 }),
      ),
    );
    const evalCalls = vi.mocked(parametersApi.update).mock.calls;
    const evalCall = evalCalls[evalCalls.length - 1][0] as Record<string, unknown>;
    expect(Object.keys(evalCall).every((k) => k.startsWith('evaluation.'))).toBe(true);
  });

  it('submits only keys present in the form (zero write-back for unset keys)', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({});
    vi.mocked(parametersApi.update).mockResolvedValue({});

    render(<PlatformSettingsPage />);
    await screen.findByText('记忆丰富温度');

    fireEvent.click(screen.getByRole('button', { name: '保存记忆参数' }));

    await waitFor(() => expect(parametersApi.update).toHaveBeenCalledWith({}));
  });

  it('renders a page-level skeleton while loading and no editable page chrome', async () => {
    vi.mocked(parametersApi.schema).mockImplementation(() => new Promise(() => {}));
    vi.mocked(parametersApi.list).mockImplementation(() => new Promise(() => {}));

    render(<PlatformSettingsPage />);

    // 只渲染明确加载态，不出现"保存X参数"等半成品展示页元素。
    expect(document.querySelector('.ant-skeleton')).toBeTruthy();
    expect(screen.queryByRole('button', { name: '保存记忆参数' })).not.toBeInTheDocument();
  });

  it('submits empty embedding_model value to clear it back to unset (fail-closed)', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue([
      ...defs(),
      {
        key: 'memory.embedding_model',
        scope: 'platform',
        category: '记忆',
        display_name: '记忆嵌入模型',
        value_type: 'string',
        default: '',
        description: '',
        optimizable: false,
        sensitive: false,
        visual_hint: { control: 'embedding_model' },
      },
    ]);
    // 平台值已清空（空串）→ 保存时显式提交空串 = 主动未配置（fail-closed），
    // 不做"等于默认跳过"。
    vi.mocked(parametersApi.list).mockResolvedValue({ 'memory.embedding_model': '' });
    vi.mocked(parametersApi.update).mockResolvedValue({});

    render(<PlatformSettingsPage />);
    await screen.findByText('记忆嵌入模型');

    fireEvent.click(screen.getByRole('button', { name: '保存记忆参数' }));

    await waitFor(() =>
      expect(parametersApi.update).toHaveBeenCalledWith(
        expect.objectContaining({ 'memory.embedding_model': '' }),
      ),
    );
  });
});
