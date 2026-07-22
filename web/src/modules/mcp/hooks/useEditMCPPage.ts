import { message } from 'antd';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { mcpApi } from '../api/mcp.api';
import type { MCPServerConfig, MCPServerConfigResponse } from '../model/mcp';

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

export const configToFormValues = (cfg: MCPServerConfigResponse) => ({
  name: cfg.name,
  version: cfg.version,
  transport: cfg.transport,
  timeout_sec: cfg.timeout ? cfg.timeout / 1e9 : MCP_DEFAULT_TIMEOUT_SEC,
  command: cfg.command,
  args: cfg.args?.join(' '),
  env: cfg.env ? Object.entries(cfg.env).map(([k, v]) => `${k}=${v}`).join('\n') : '',
  url: cfg.url,
  headers: cfg.headers
    ? Object.entries(cfg.headers)
        .map(([k, v]) => `${k}: ${v}`)
        .join('\n')
    : '',
  auth_type: cfg.auth?.type ?? 'none',
  api_key_header: cfg.auth?.api_key_header,
  oauth2_client_id: cfg.auth?.oauth2_client_id,
  oauth2_token_url: cfg.auth?.oauth2_token_url,
  oauth2_scopes: cfg.auth?.oauth2_scopes?.join(', '),
  bearer_token: undefined,
  api_key_value: undefined,
  oauth2_client_secret: undefined,
  credential_configured: cfg.auth?.credential_configured ?? false,
  retry_enabled: cfg.retry?.enabled ?? false,
  retry_max_retries: cfg.retry?.max_retries ?? MCP_RETRY_MAX_RETRIES,
  retry_initial_delay_ms: cfg.retry?.initial_delay_ms ?? MCP_RETRY_INITIAL_DELAY_MS,
  retry_max_delay_ms: cfg.retry?.max_delay_ms ?? MCP_RETRY_MAX_DELAY_MS,
  retry_backoff_factor: cfg.retry?.backoff_factor ?? MCP_RETRY_BACKOFF_FACTOR,
});

export interface FormValues {
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

export const buildMCPUpdateConfig = (id: string, values: FormValues): MCPServerConfig => {
  const cfg: MCPServerConfig = {
    id,
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
      if (authType === 'bearer' && values.bearer_token) {
        cfg.auth.token = values.bearer_token;
      } else if (authType === 'api_key') {
        cfg.auth.api_key_header = values.api_key_header || 'X-API-Key';
        if (values.api_key_value) cfg.auth.api_key_value = values.api_key_value;
      } else if (authType === 'oauth2') {
        cfg.auth.oauth2_client_id = values.oauth2_client_id || '';
        if (values.oauth2_client_secret) cfg.auth.oauth2_client_secret = values.oauth2_client_secret;
        cfg.auth.oauth2_token_url = values.oauth2_token_url || '';
        cfg.auth.oauth2_scopes = (values.oauth2_scopes || '').split(/[,\s]+/).filter(Boolean);
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

  return cfg;
};

export const useEditMCPPage = (id: string) => {
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [initialValues, setInitialValues] = useState<ReturnType<typeof configToFormValues> | null>(null);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const cfg = await mcpApi.getConfig(id);
        if (!cancelled) setInitialValues(configToFormValues(cfg));
      } catch {
        if (!cancelled) message.error('加载配置失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [id]);

  const handleFinish = async (values: FormValues) => {
    setSubmitting(true);
    try {
      await mcpApi.update(id, buildMCPUpdateConfig(id, values));
      message.success('MCP 服务器配置已更新并重新连接');
      navigate('/mcp');
    } catch (err) {
      message.error(extractErrorMessage(err) || '更新失败');
    } finally {
      setSubmitting(false);
    }
  };

  return { loading, submitting, initialValues, handleFinish };
};
