import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { ServerDetailDrawer } from '../ServerDetailDrawer';

const responsive = vi.hoisted(() => ({ isMobile: true }));
const api = vi.hoisted(() => ({ tools: vi.fn(), resources: vi.fn() }));

vi.mock('@/shared/hooks', () => ({ useResponsive: () => responsive }));
vi.mock('../../api/mcp.api', () => ({ mcpApi: api }));

const server = {
  id: 'server-1',
  name: '知识服务',
  transport: 'http',
  status: 'connected',
  version: '1.0',
};

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
});

describe('ServerDetailDrawer responsive data', () => {
  beforeEach(() => {
    responsive.isMobile = true;
    api.tools.mockResolvedValue([{ name: 'search', description: '检索知识库' }]);
    api.resources.mockResolvedValue([{ uri: 'kb://manual', name: '产品手册', mimeType: 'text/plain' }]);
  });

  it('renders tool and resource mobile cards', async () => {
    render(<ServerDetailDrawer server={server} onClose={vi.fn()} />);

    expect(await screen.findByText('search')).toBeInTheDocument();
    expect(screen.getByText('检索知识库')).toBeInTheDocument();
    expect(document.querySelector('.ant-table')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: /资源/ }));
    expect(await screen.findByText('产品手册')).toBeInTheDocument();
    expect(screen.getByText('kb://manual')).toBeInTheDocument();
    expect(screen.getByText('text/plain')).toBeInTheDocument();
  });

  it('keeps small desktop tables', async () => {
    responsive.isMobile = false;
    render(<ServerDetailDrawer server={server} onClose={vi.fn()} />);

    await waitFor(() => expect(document.querySelector('.ant-table-small')).toBeInTheDocument());
  });
});
