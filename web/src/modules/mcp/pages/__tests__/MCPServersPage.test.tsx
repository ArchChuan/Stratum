import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { MCPServersPage } from '../MCPServersPage';

const responsive = vi.hoisted(() => ({ isMobile: true }));
const page = vi.hoisted(() => ({
  setDetailServer: vi.fn(),
  fetchServers: vi.fn(),
  handleDisconnect: vi.fn(),
  handleReconnect: vi.fn(),
  handleDelete: vi.fn(),
}));
const navigate = vi.hoisted(() => vi.fn());

vi.mock('@/shared/hooks', () => ({ useResponsive: () => responsive }));
vi.mock('react-router-dom', () => ({ useNavigate: () => navigate }));
vi.mock('../../hooks/useMCPServersPage', () => ({
  useMCPServersPage: () => ({
    servers: [
      {
        id: 'server-1',
        name: '搜索服务',
        transport: 'http',
        status: 'disconnected',
        url: 'https://mcp.example.test',
        tools: [{ name: 'search', description: '搜索' }],
      },
      {
        id: 'server-2',
        name: '文件服务',
        transport: 'stdio',
        status: 'connected',
        command: 'mcp-files',
        tools: [],
      },
    ],
    loading: false,
    detailServer: null,
    ...page,
  }),
}));
vi.mock('../../components/ServerDetailDrawer', () => ({ ServerDetailDrawer: () => null }));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
});

describe('MCPServersPage responsive list', () => {
  beforeEach(() => {
    responsive.isMobile = true;
    vi.clearAllMocks();
  });

  it('renders labelled mobile actions and delegates callbacks', async () => {
    render(<MCPServersPage />);

    expect(screen.getByText('搜索服务')).toBeInTheDocument();
    expect(screen.getByText('http')).toBeInTheDocument();
    expect(screen.getByText('https://mcp.example.test')).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: '查看详情' })[0]);
    fireEvent.click(screen.getAllByRole('button', { name: '编辑服务器' })[0]);
    fireEvent.click(screen.getByRole('button', { name: '连接服务器' }));
    expect(page.setDetailServer).toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith('/mcp/server-1/edit');
    expect(page.handleReconnect).toHaveBeenCalledWith('server-1');
    fireEvent.click(screen.getAllByRole('button', { name: '删除服务器' })[0]);
    const deleteConfirmation = await screen.findByText('确认删除此服务器配置？关联的 Agent 将无法再使用此服务器。');
    fireEvent.click(
      within(deleteConfirmation.closest('.ant-popover') as HTMLElement)
        .getByRole('button', { name: /删\s*除/ }),
    );
    expect(page.handleDelete).toHaveBeenCalledWith('server-1');

    fireEvent.click(screen.getByRole('button', { name: '断开连接' }));
    const disconnectConfirmation = await screen.findByText('确认断开此服务器连接？');
    fireEvent.click(
      within(disconnectConfirmation.closest('.ant-popover') as HTMLElement)
        .getByRole('button', { name: /断\s*开/ }),
    );
    expect(page.handleDisconnect).toHaveBeenCalledWith('server-2');
    expect(document.querySelector('.ant-table')).not.toBeInTheDocument();
  });

  it('keeps the desktop table', () => {
    responsive.isMobile = false;
    render(<MCPServersPage />);
    expect(document.querySelector('.ant-table')).toBeInTheDocument();
  });
});
