import { describe, expect, it } from 'vitest';

import type { MCPServerConfigResponse } from '../model/mcp';

import { buildMCPUpdateConfig, configToFormValues } from './useEditMCPPage';

const redactedConfig: MCPServerConfigResponse = {
  id: 'server-1',
  name: 'private server',
  version: '1',
  transport: 'http',
  timeout: 30e9,
  url: 'https://mcp.example.com',
  auth: {
    type: 'api_key',
    api_key_header: 'X-API-Key',
    credential_configured: true,
  },
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
