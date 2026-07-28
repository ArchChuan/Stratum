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
  },
}));
vi.mock('@/modules/iam', () => ({ useAuth: () => ({ user: { role: 'admin' } }) }));
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
const Harness = () => {
  const { configForm } = useKnowledgeDetailPage();
  return <Form form={configForm}><Form.Item label="Top-K" name="top_k"><InputNumber /></Form.Item></Form>;
};

describe('useKnowledgeDetailPage', () => {
  beforeEach(() => {
    vi.mocked(knowledgeApi.stats).mockReset();
    vi.mocked(knowledgeApi.listDocuments).mockReset().mockResolvedValue([]);
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
});
