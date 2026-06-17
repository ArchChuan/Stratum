import { message } from 'antd';
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { mcpApi } from '../api/mcp.api';
import type { MCPServerConfig } from '../model/mcp';

import {
  MCP_DEFAULT_TIMEOUT_SEC,
  MCP_RETRY_BACKOFF_FACTOR,
  MCP_RETRY_INITIAL_DELAY_MS,
  MCP_RETRY_MAX_DELAY_MS,
  MCP_RETRY_MAX_RETRIES,
} from '@/constants';
import { extractErrorMessage } from '@/shared/lib';

const parseEnv = (str?: string): Record<string, string> => {
  const result: Record<string, string> = {};
  if (!str) return result;
  for (const line of str.split('\n')) {
    const eq = line.indexOf('=');
    if (eq > 0) result[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return result;
};

const parseKV = (str?: string): Record<string, string> => {
  const result: Record<string, string> = {};
  if (!str) return result;
  for (const line of str.split('\n')) {
    const eq = line.indexOf(':');
    if (eq > 0) result[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return result;
};

const parseArgs = (str?: string): string[] => (str || '').split(/\s+/).filter(Boolean);

interface FormValues {
  name: string;
  version?: string;
  transport: string;
  timeout_sec?: number;
  command?: string;
  args?: string;
  env?: string;
  url?: string;
  headers?: string;
  auth_type?: string;
  bearer_token?: string;
  api_key_header?: string;
  api_key_value?: string;
  oauth2_client_id?: string;
  oauth2_client_secret?: string;
  oauth2_token_url?: string;
  oauth2_scopes?: string;
  retry_enabled?: boolean;
  retry_max_retries?: number;
  retry_initial_delay_ms?: number;
  retry_max_delay_ms?: number;
  retry_backoff_factor?: number;
}

export const useCreateMCPPage = () => {
  const [submitting, setSubmitting] = useState(false);
  const navigate = useNavigate();

  const handleFinish = async (values: FormValues) => {
    setSubmitting(true);
    try {
      const cfg: MCPServerConfig = {
        id: crypto.randomUUID(),
        name: values.name,
        version: values.version || '',
        transport: values.transport,
        timeout: (values.timeout_sec || MCP_DEFAULT_TIMEOUT_SEC) * 1e9,
      };

      if (values.transport === 'stdio') {
        cfg.command = values.command || '';
        cfg.args = parseArgs(values.args);
        cfg.env = parseEnv(values.env);
      } else {
        cfg.url = values.url || '';
        cfg.headers = parseKV(values.headers);
        const authType = values.auth_type || 'none';
        if (authType !== 'none') {
          cfg.auth = { type: authType };
          if (authType === 'bearer') {
            cfg.auth.token = values.bearer_token || '';
          } else if (authType === 'api_key') {
            cfg.auth.api_key_header = values.api_key_header || 'X-API-Key';
            cfg.auth.api_key_value = values.api_key_value || '';
          } else if (authType === 'oauth2') {
            cfg.auth.oauth2_client_id = values.oauth2_client_id || '';
            cfg.auth.oauth2_client_secret = values.oauth2_client_secret || '';
            cfg.auth.oauth2_token_url = values.oauth2_token_url || '';
            cfg.auth.oauth2_scopes = (values.oauth2_scopes || '')
              .split(/[,\s]+/)
              .filter(Boolean);
          }
        }
      }

      if (values.retry_enabled) {
        cfg.retry = {
          enabled: true,
          max_retries: values.retry_max_retries ?? MCP_RETRY_MAX_RETRIES,
          initial_delay_ms: values.retry_initial_delay_ms ?? MCP_RETRY_INITIAL_DELAY_MS,
          max_delay_ms: values.retry_max_delay_ms ?? MCP_RETRY_MAX_DELAY_MS,
          backoff_factor: values.retry_backoff_factor ?? MCP_RETRY_BACKOFF_FACTOR,
        };
      }

      await mcpApi.connect(cfg);
      message.success('MCP 服务器已添加');
      navigate('/mcp');
    } catch (err) {
      message.error(extractErrorMessage(err) || '添加失败');
    } finally {
      setSubmitting(false);
    }
  };

  return { submitting, handleFinish };
};
