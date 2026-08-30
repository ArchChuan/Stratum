import { renderHook, waitFor } from '@testing-library/react';
import { message } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useKnowledgeDetailPage } from '../useKnowledgeDetailPage';

const membersMock = vi.hoisted(() => vi.fn());
const statsMock = vi.hoisted(() => vi.fn());
const listDocumentsMock = vi.hoisted(() => vi.fn());
// 可变 user 供 I-1 派生值用例切换 role/sub
const userMock = vi.hoisted(() => ({ role: 'admin', sub: 'u-admin' }));

vi.mock('react-router-dom', () => ({
  useParams: () => ({ name: 'test-ws' }),
  useNavigate: () => vi.fn(),
}));

vi.mock('@/modules/iam', () => ({
  tenantApi: { members: membersMock },
  useAuth: () => ({ user: userMock }),
}));

// mock 路径相对本测试文件解析：__tests__/ 下需两级上溯到 modules/knowledge/。
// 修复前 '../api/knowledge.api' 解析到不存在的 hooks/api/，mock 未生效，真实
// axios 打到 dev server（ECONNREFUSED 127.0.0.1:3000）。
vi.mock('../../api/knowledge.api', () => ({
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

describe('useKnowledgeDetailPage I-1 编辑权派生', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(message, 'error').mockImplementation(() => undefined as never);
    vi.spyOn(message, 'success').mockImplementation(() => undefined as never);
    statsMock.mockResolvedValue({ config: {}, editors: ['u-1'] });
    listDocumentsMock.mockResolvedValue([]);
    membersMock.mockResolvedValue({ members: [], total: 0, page: 1, page_size: 1 });
  });

  it('member + in-editors → canEdit=true / canRequestEditor=false', async () => {
    userMock.role = 'member';
    userMock.sub = 'u-1';
    const { result } = renderHook(() => useKnowledgeDetailPage());
    await waitFor(() => expect(result.current.stats).not.toBeNull());
    expect(result.current.isAdmin).toBe(false);
    expect(result.current.canEdit).toBe(true);
    expect(result.current.canRequestEditor).toBe(false);
  });

  it('member + not-in-editors → canEdit=false / canRequestEditor=true', async () => {
    userMock.role = 'member';
    userMock.sub = 'u-9';
    const { result } = renderHook(() => useKnowledgeDetailPage());
    await waitFor(() => expect(result.current.stats).not.toBeNull());
    expect(result.current.isAdmin).toBe(false);
    expect(result.current.canEdit).toBe(false);
    expect(result.current.canRequestEditor).toBe(true);
  });

  it('admin → canEdit=true / canRequestEditor=false', async () => {
    userMock.role = 'admin';
    userMock.sub = 'u-admin';
    const { result } = renderHook(() => useKnowledgeDetailPage());
    await waitFor(() => expect(result.current.stats).not.toBeNull());
    expect(result.current.isAdmin).toBe(true);
    expect(result.current.canEdit).toBe(true);
    expect(result.current.canRequestEditor).toBe(false);
  });
});
