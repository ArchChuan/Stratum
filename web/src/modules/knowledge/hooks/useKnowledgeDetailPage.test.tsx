import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Form, InputNumber } from 'antd';
import { StrictMode, type PropsWithChildren } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { knowledgeApi } from '../api/knowledge.api';

import { useKnowledgeDetailPage } from './useKnowledgeDetailPage';

vi.mock('../api/knowledge.api', () => ({
  knowledgeApi: {
    stats: vi.fn(),
    listDocuments: vi.fn(),
    update: vi.fn(),
  },
}));
// hook 通过 @/modules/llm（index.ts）引用 llmApi —— 走 barrel mock，否则真实 getCatalogue fetch 网络挂测试
vi.mock('@/modules/llm', () => ({
  llmApi: {
    getCatalogue: vi.fn().mockResolvedValue({ chatModels: ['qwen-turbo'], embeddingModels: ['text-embedding-v3'] }),
  },
}));
vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { role: 'admin' } }),
  tenantApi: { members: vi.fn().mockResolvedValue({ members: [], total: 0, page: 1, page_size: 1000 }) },
}));
vi.mock('react-router-dom', () => ({
  useNavigate: () => vi.fn(),
  useParams: () => ({ name: 'workspace-1' }),
}));

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};
const stats = {
  description: '知识库',
  config: { embedding_model: 'text-embedding-v1', chunking_strategy: 'fixed', top_k: 5 },
};
const wrapper = ({ children }: PropsWithChildren) => <StrictMode>{children}</StrictMode>;

// Harness 暴露 page 句柄（含 handleConfigSave），供断言保存 payload 的 rerank/judge 构造
let pageRef: ReturnType<typeof useKnowledgeDetailPage>;
const Harness = () => {
  const page = useKnowledgeDetailPage();
  pageRef = page;
  return <Form form={page.configForm}><Form.Item label="Top-K" name="top_k"><InputNumber /></Form.Item></Form>;
};

describe('useKnowledgeDetailPage', () => {
  beforeEach(() => {
    vi.mocked(knowledgeApi.stats).mockReset();
    vi.mocked(knowledgeApi.listDocuments).mockReset().mockResolvedValue([]);
    vi.mocked(knowledgeApi.update).mockReset().mockResolvedValue(undefined as never);
  });

  it('does not overwrite an edited config field when an older stats request resolves', async () => {
    const first = deferred<typeof stats>();
    const second = deferred<typeof stats>();
    vi.mocked(knowledgeApi.stats).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    render(wrapper({ children: <Harness /> }));

    await act(async () => first.resolve(stats));
    const input = await screen.findByRole('spinbutton', { name: 'Top-K' });
    await waitFor(() => expect(input).toHaveValue('5'));
    fireEvent.change(input, { target: { value: '6' } });

    await act(async () => second.resolve(stats));
    await waitFor(() => expect(knowledgeApi.stats).toHaveBeenCalledTimes(2));
    expect(input).toHaveValue('6');
  });

  it('sends judge_model as "" when cleared and drops stale rerank_model for non-builtin', async () => {
    vi.mocked(knowledgeApi.stats).mockResolvedValue(stats);
    render(wrapper({ children: <Harness /> }));

    await act(async () => {
      // reranking 非 builtin：rerank_model Field 卸载（preserve=false）→ form store 无该值 → undefined
      await pageRef.handleConfigSave({
        query_mode: 'hybrid',
        top_k: 5,
        reranking: 'vector',
        judge_model: undefined,
      });
    });

    const payload = vi.mocked(knowledgeApi.update).mock.calls[0][1] as { config: Record<string, unknown> };
    // judge_model 清空必须发 ""（allowClear 置 undefined 会被 JSON 丢弃 → 后端 partial 保留旧值 → 判断门关不掉）
    expect(payload.config.judge_model).toBe('');
    expect(payload.config.rerank_model).toBeUndefined();
    // JSON 序列化丢弃 undefined → 后端 partial 合并保留旧 rerank_model（dormant），不带 stale 值
    expect(JSON.stringify(payload.config)).not.toContain('rerank_model');
  });

  it('passes rerank_model and judge_model when reranking is builtin', async () => {
    vi.mocked(knowledgeApi.stats).mockResolvedValue(stats);
    render(wrapper({ children: <Harness /> }));

    await act(async () => {
      await pageRef.handleConfigSave({
        query_mode: 'hybrid',
        top_k: 5,
        reranking: 'builtin-score-v1',
        rerank_model: 'qwen-turbo',
        judge_model: 'qwen-plus',
      });
    });

    const payload = vi.mocked(knowledgeApi.update).mock.calls[0][1] as { config: Record<string, unknown> };
    expect(payload.config.rerank_model).toBe('qwen-turbo');
    expect(payload.config.judge_model).toBe('qwen-plus');
  });
});
