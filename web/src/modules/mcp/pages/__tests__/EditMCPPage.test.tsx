import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useEditMCPPage } from '../../hooks/useEditMCPPage';
import { EditMCPPage } from '../EditMCPPage';

vi.mock('react-router-dom', () => ({
  useParams: () => ({ id: 'server-1' }),
  useNavigate: () => vi.fn(),
}));
vi.mock('../../hooks/useEditMCPPage', () => ({ useEditMCPPage: vi.fn() }));
vi.mock('../../components/MCPBasicSection', () => ({ MCPBasicSection: () => <div>基础配置</div> }));
vi.mock('../../components/MCPTransportSection', () => ({ MCPTransportSection: () => <div>传输方式</div> }));
vi.mock('../../components/MCPAuthSection', () => ({ MCPAuthSection: () => <div>鉴权配置</div> }));
vi.mock('../../components/MCPRetrySection', () => ({ MCPRetrySection: () => <div>重试配置</div> }));
vi.mock('@/shared/components', () => ({
  RequestEditorButton: ({ resourceType, resourceId }: { resourceType: string; resourceId: string }) => (
    <button type="button">申请编辑权限-{resourceType}-{resourceId}</button>
  ),
}));

const baseHook = {
  loading: false,
  submitting: false,
  initialValues: {},
  handleFinish: vi.fn(),
};

const renderPage = (overrides: Partial<ReturnType<typeof useEditMCPPage>> = {}) => {
  vi.mocked(useEditMCPPage).mockReturnValue({
    ...baseHook,
    canEdit: true,
    editors: [],
    ...overrides,
  } as unknown as ReturnType<typeof useEditMCPPage>);
  return render(<EditMCPPage />);
};

describe('EditMCPPage readOnly', () => {
  beforeEach(() => vi.clearAllMocks());

  it('canEdit 时显示「保存并重连」，不显示申请入口', () => {
    renderPage({ canEdit: true });

    expect(screen.getByRole('button', { name: '保存并重连' })).toBeInTheDocument();
    expect(screen.queryByText(/申请编辑权限/)).not.toBeInTheDocument();
  });

  it('非白名单成员只读：表单可查看但隐藏提交按钮，并显示申请入口', () => {
    renderPage({ canEdit: false });

    expect(screen.getByText('基础配置')).toBeInTheDocument();
    expect(screen.getByText('传输方式')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '保存并重连' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '申请编辑权限-mcp-server-1' })).toBeInTheDocument();
  });
});
