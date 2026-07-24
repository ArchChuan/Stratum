import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { PrivateRoute } from './PrivateRoute';

const auth = vi.hoisted(() => ({ user: null, loading: true }));
vi.mock('./AuthContext', () => ({ useAuth: () => auth }));

describe('PrivateRoute', () => {
  let consoleError: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    auth.user = null;
    auth.loading = true;
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
  });

  afterEach(() => { consoleError.mockRestore(); });

  it('renders an accessible loading state without an Ant Design Spin warning', () => {
    render(<MemoryRouter><PrivateRoute><div>受保护内容</div></PrivateRoute></MemoryRouter>);

    expect(screen.getByRole('status', { name: '加载中...' })).toBeInTheDocument();
    expect(consoleError).not.toHaveBeenCalledWith(expect.stringContaining('[antd: Spin]'));
  });
});
