import { renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { mcpApi } from '../api/mcp.api';
import type { MCPServerConfigResponse } from '../model/mcp';

import { buildMCPUpdateConfig, configToFormValues, useEditMCPPage } from './useEditMCPPage';

vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }));
vi.mock('../api/mcp.api', () => ({ mcpApi: { getConfig: vi.fn(), update: vi.fn() } }));
vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { sub: 'u-1' } }),
  useTenantRole: () => ({ role: 'member', isAdmin: false, isOwner: false, isMember: true, hasTenantRole: () => false }),
}));

const redactedConfig: MCPServerConfigResponse = {
  id: 'server-1',
  name: 'private server',
  version: '1',
  transport: 'http',
  command: '',
  args: [],
  env: {},
  url: 'https://mcp.example.com',
  capabilities: [],
  timeout: 30e9,
  auth: {
    type: 'api_key',
    api_key_header: 'X-API-Key',
    credential_configured: true,
  },
  editors: [],
};

describe('MCP edit credential handling', () => {
  it('keeps replacement fields empty for a redacted response', () => {
    const values = configToFormValues(redactedConfig);

    expect(values.api_key_value).toBeUndefined();
    expect(values.bearer_token).toBeUndefined();
    expect(values.oauth2_client_secret).toBeUndefined();
    expect(values.credential_configured).toBe(true);
  });

  it('omits an empty API key replacement from the update payload', () => {
    const payload = buildMCPUpdateConfig('server-1', {
      name: 'private server',
      version: '1',
      transport: 'http',
      timeout_sec: 30,
      url: 'https://mcp.example.com',
      auth_type: 'api_key',
      api_key_header: 'X-API-Key',
      api_key_value: '',
    });

    expect(payload.auth).toEqual({ type: 'api_key', api_key_header: 'X-API-Key' });
    expect(payload.auth).not.toHaveProperty('api_key_value');
  });

  it('includes a supplied credential replacement', () => {
    const payload = buildMCPUpdateConfig('server-1', {
      name: 'private server',
      transport: 'http',
      auth_type: 'bearer',
      bearer_token: 'replacement-value',
    });

    expect(payload.auth).toEqual({ type: 'bearer', token: 'replacement-value' });
  });
});

const editorConfig = (editors: string[]): MCPServerConfigResponse => ({
  id: 'server-1',
  name: 'private server',
  version: '1',
  transport: 'http',
  command: '',
  args: [],
  env: {},
  url: 'https://mcp.example.com',
  capabilities: [],
  timeout: 30e9,
  editors,
});

describe('useEditMCPPage canEdit', () => {
  it('白名单成员（命中 config.editors）可编辑', async () => {
    vi.mocked(mcpApi.getConfig).mockResolvedValue(editorConfig(['u-1']));
    const { result } = renderHook(() => useEditMCPPage('server-1'));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.editors).toEqual(['u-1']);
    expect(result.current.canEdit).toBe(true);
  });

  it('非白名单普通成员只读', async () => {
    vi.mocked(mcpApi.getConfig).mockResolvedValue(editorConfig([]));
    const { result } = renderHook(() => useEditMCPPage('server-1'));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canEdit).toBe(false);
  });
});
