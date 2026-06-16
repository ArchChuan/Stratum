import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { message } from 'antd';
import { connectMCPServer } from '../services/mcp';
import {
  MCP_DEFAULT_TIMEOUT_SEC,
  MCP_RETRY_INITIAL_DELAY_MS,
  MCP_RETRY_MAX_DELAY_MS,
  MCP_RETRY_MAX_RETRIES,
  MCP_RETRY_BACKOFF_FACTOR,
} from '../constants';

function parseEnv(str) {
  const result = {};
  if (!str) return result;
  str.split('\n').forEach(line => {
    const eq = line.indexOf('=');
    if (eq > 0) result[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  });
  return result;
}

function parseKV(str) {
  const result = {};
  if (!str) return result;
  str.split('\n').forEach(line => {
    const eq = line.indexOf(':');
    if (eq > 0) result[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  });
  return result;
}

function parseArgs(str) {
  return (str || '').split(/\s+/).filter(Boolean);
}

const useCreateMCPPage = () => {
  const [submitting, setSubmitting] = useState(false);
  const navigate = useNavigate();

  const handleFinish = async (values) => {
    setSubmitting(true);
    try {
      const cfg = {
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
        // auth
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
            cfg.auth.oauth2_scopes = (values.oauth2_scopes || '').split(/[,\s]+/).filter(Boolean);
          }
        }
      }

      // retry
      if (values.retry_enabled) {
        cfg.retry = {
          enabled: true,
          max_retries: values.retry_max_retries ?? MCP_RETRY_MAX_RETRIES,
          initial_delay_ms: values.retry_initial_delay_ms ?? MCP_RETRY_INITIAL_DELAY_MS,
          max_delay_ms: values.retry_max_delay_ms ?? MCP_RETRY_MAX_DELAY_MS,
          backoff_factor: values.retry_backoff_factor ?? MCP_RETRY_BACKOFF_FACTOR,
        };
      }

      await connectMCPServer(cfg);
      message.success('MCP 服务器已添加');
      navigate('/mcp');
    } catch (err) {
      message.error(err.response?.data?.error || '添加失败');
    } finally {
      setSubmitting(false);
    }
  };

  return { submitting, handleFinish };
};

export default useCreateMCPPage;
