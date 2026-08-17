import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { parametersApi } from '../../api/parameters.api';
import type { ParameterDefinition } from '../../model/parameters';
import { PlatformSettingsPage } from '../PlatformSettingsPage';

vi.mock('../../api/parameters.api', () => ({
  parametersApi: { schema: vi.fn(), list: vi.fn(), update: vi.fn(), promptDefaults: vi.fn() },
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

describe('PlatformSettingsPage', () => {
  it('shows 默认：0（未设置） for a missing key with 0 default, and no hint for set keys', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    // 非 0 默认键被后端回填(已设置),0 默认键缺失
    vi.mocked(parametersApi.list).mockResolvedValue({ 'memory.enrich_temperature': 0.9 });

    render(<PlatformSettingsPage />);

    expect(await screen.findByText('评测温度')).toBeInTheDocument();
    expect(screen.getByText('默认：0（未设置）')).toBeInTheDocument();
    expect(screen.queryByText('默认：0.1')).not.toBeInTheDocument();
  });

  it('shows the non-zero default hint when the key is missing entirely', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({});

    render(<PlatformSettingsPage />);

    expect(await screen.findByText('默认：0.1')).toBeInTheDocument();
    expect(screen.getByText('默认：0（未设置）')).toBeInTheDocument();
  });

  it('shows 未设置（使用定义默认） for string keys with empty default and renders prompt viewers', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({});

    render(<PlatformSettingsPage />);

    expect(await screen.findByText('记忆取代提示词')).toBeInTheDocument();
    expect(screen.getAllByText('未设置（使用定义默认）')).toHaveLength(2);
    expect(screen.getAllByRole('button', { name: '查看默认提示词' })).toHaveLength(2);
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

    expect(await screen.findByText('RAG 开关')).toBeInTheDocument();
    // toggle 恒被 List 返回,无缺失场景,不渲染 hint(副标题含"未设置"文案,故精确匹配 hint 文本)
    expect(screen.queryByText('默认：', { exact: false })).not.toBeInTheDocument();
    expect(screen.queryByText('未设置（使用定义默认）')).not.toBeInTheDocument();
  });

  it('submits only keys present in the form (zero write-back for unset keys)', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({});
    vi.mocked(parametersApi.update).mockResolvedValue({});

    render(<PlatformSettingsPage />);
    await screen.findByText('评测温度');

    fireEvent.click(screen.getByRole('button', { name: '保存平台参数' }));

    await waitFor(() => expect(parametersApi.update).toHaveBeenCalledWith({}));
  });
});
