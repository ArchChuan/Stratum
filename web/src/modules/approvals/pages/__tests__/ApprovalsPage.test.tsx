import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useSearchParams } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApprovalsPage } from '../ApprovalsPage';

vi.mock('@/modules/iam', () => ({ useTenantRole: () => ({ isAdmin: true }) }));
// 组件树中 antd Tabs 默认挂载全部 pane，OperationProposalsPanel/approvalColumns
// 等 import 会解析到真实 api 模块；mock client 拦截任何 XHR，避免 jsdom 下向
// localhost:3000 发请求（ECONNREFUSED 回调在 teardown 后 setState 报 window is not defined）。
vi.mock('@/services/client', () => ({
  default: { get: vi.fn(() => new Promise(() => {})) },
}));
vi.mock('../hooks/useApprovalsPage', () => ({
  useApprovalsPage: () => ({
    activeTab: 'pending', pendingRows: [], pendingLoading: false, historyRows: [],
    historyLoading: false, total: 0, page: 1, pageSize: 20, pageSizeOptions: ['10', '20', '50'],
    detail: null, detailLoading: false, approvers: [], approversLoading: false,
    switchTab: vi.fn(), handleHistoryPageChange: vi.fn(), openDetail: vi.fn(),
    closeDetail: vi.fn(), decide: vi.fn(), execute: vi.fn(), assign: vi.fn(),
    loadApprovers: vi.fn(), isActionLoading: () => false,
  }),
}));
vi.mock('@/modules/operation-gate/components/OperationProposalsPanel', () => ({
  OperationProposalsPanel: ({ readonly }: { readonly?: boolean }) => (
    <div>权限面板{readonly ? '(只读)' : ''}</div>
  ),
}));
vi.mock('../components/DecideApprovalModal', () => ({ DecideApprovalModal: () => null }));
vi.mock('../components/ApprovalDetailDrawer', () => ({ ApprovalDetailDrawer: () => null }));

// 模拟铃铛等外部 navigate('/approvals?tab=permission')：不改 ApprovalsPage state，
// 只改 URL query，验证页面是否随 searchParams 同步顶层 tab（bugfix 回归）。
const GoPermission = () => {
  const [, setSearchParams] = useSearchParams();
  return <button onClick={() => setSearchParams({ tab: 'permission' })}>go-permission</button>;
};

const renderPage = (initialPath = '/approvals') =>
  render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/approvals" element={<><GoPermission /><ApprovalsPage /></>} />
      </Routes>
    </MemoryRouter>,
  );

describe('ApprovalsPage 顶层 tab 与 URL 同步', () => {
  beforeEach(() => vi.clearAllMocks());

  it('无 query 默认工具审批，权限面板不渲染', () => {
    renderPage();
    expect(screen.getByRole('heading', { name: '工具审批' })).toBeVisible();
    expect(screen.queryByText('权限面板')).not.toBeInTheDocument();
  });

  it('初始 ?tab=permission 直接渲染权限面板', () => {
    renderPage('/approvals?tab=permission');
    expect(screen.getByText('权限面板')).toBeVisible();
  });

  it('外部导航改 URL 后同步切换顶层 tab（铃铛跳转回归）', async () => {
    renderPage();
    expect(screen.queryByText('权限面板')).not.toBeInTheDocument();

    act(() => {
      screen.getByRole('button', { name: 'go-permission' }).click();
    });
    await waitFor(() => expect(screen.getByText('权限面板')).toBeVisible());
    expect(screen.queryByRole('heading', { name: '工具审批' })).not.toBeInTheDocument();
  });
});
