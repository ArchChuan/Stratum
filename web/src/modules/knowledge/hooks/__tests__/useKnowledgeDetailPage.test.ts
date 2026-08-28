import { renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useKnowledgeDetailPage } from '../useKnowledgeDetailPage';

const membersMock = vi.hoisted(() => vi.fn());
const statsMock = vi.hoisted(() => vi.fn());
const listDocumentsMock = vi.hoisted(() => vi.fn());

vi.mock('react-router-dom', () => ({
  useParams: () => ({ name: 'test-ws' }),
  useNavigate: () => vi.fn(),
}));

vi.mock('@/modules/iam', () => ({
  tenantApi: { members: membersMock },
  useAuth: () => ({ user: { role: 'admin' } }),
}));

vi.mock('../api/knowledge.api', () => ({
  knowledgeApi: {
    stats: statsMock,
    listDocuments: listDocumentsMock,
    update: vi.fn(),
    ingest: vi.fn(),
    query: vi.fn(),
    deleteDocument: vi.fn(),
    setDocAccess: vi.fn(),
  },
}));

describe('useKnowledgeDetailPage 指定角色候选', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
    vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
    statsMock.mockResolvedValue({ config: {} });
    listDocumentsMock.mockResolvedValue([]);
  });

  it('成员角色单一的小型租户仍提供完整角色集', async () => {
    membersMock.mockResolvedValue({
      members: [{ user_id: 'u1', github_login: 'alice', role: 'owner' }],
      total: 1,
      page: 1,
      page_size: 1,
    });
    const { result } = renderHook(() => useKnowledgeDetailPage());
    await waitFor(() => expect(result.current.userCandidatesLoading).toBe(false));
    expect(result.current.roleCandidates).toEqual(['admin', 'owner', 'member']);
  });

  it('默认租户成员出现 root 角色时并入候选', async () => {
    membersMock.mockResolvedValue({
      members: [{ user_id: 'u1', role: 'root' }],
      total: 1,
      page: 1,
      page_size: 1,
    });
    const { result } = renderHook(() => useKnowledgeDetailPage());
    await waitFor(() => expect(result.current.userCandidatesLoading).toBe(false));
    expect(result.current.roleCandidates).toEqual(['admin', 'owner', 'member', 'root']);
  });

  it('成员接口失败时保留兜底角色集', async () => {
    membersMock.mockRejectedValue(new Error('members failed'));
    const { result } = renderHook(() => useKnowledgeDetailPage());
    await waitFor(() => expect(result.current.userCandidatesLoading).toBe(false));
    expect(result.current.roleCandidates).toEqual(['admin', 'owner', 'member']);
  });
});
