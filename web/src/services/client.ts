import { message } from 'antd';
import axios, {
  type AxiosInstance,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from 'axios';

import { API_DEFAULT_TIMEOUT_MS } from '@/constants';

interface RetryableConfig extends InternalAxiosRequestConfig {
  _retry?: boolean;
}

type TokenRef = { current: string | null };
type LogoutHandler = () => void;
type StreamEventHandler = (event: unknown) => boolean | void;

const api: AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '',
  timeout: API_DEFAULT_TIMEOUT_MS,
  withCredentials: true,
});

let _tokenRef: TokenRef = { current: null };
let _reqInterceptor: number | null = null;
let _resInterceptor: number | null = null;

let _authReady = false;
let _authReadyResolve: (() => void) | null = null;
let _authReadyPromise: Promise<void> = new Promise<void>((resolve) => {
  _authReadyResolve = resolve;
});

const AUTH_READY_TIMEOUT_MS = 8000;

const PUBLIC_PATH_PREFIXES = [
  '/auth/refresh',
  '/auth/logout',
  '/auth/github',
  '/auth/guest',
  '/auth/register',
  '/auth/callback',
  '/health',
  '/metrics',
  '/models',
];

const isPublicRequest = (url: string | undefined): boolean => {
  if (!url) return false;
  return PUBLIC_PATH_PREFIXES.some((p) => url === p || url.startsWith(`${p}?`) || url.startsWith(`${p}/`));
};

const waitWithTimeout = <T,>(p: Promise<T>, ms: number): Promise<T> =>
  new Promise<T>((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('auth ready timeout')), ms);
    p.then((v) => { clearTimeout(t); resolve(v); }, (e) => { clearTimeout(t); reject(e); });
  });

export const markAuthReady = (): void => {
  if (_authReady) return;
  _authReady = true;
  _authReadyResolve?.();
};

export const resetAuthReady = (): void => {
  _authReady = false;
  _authReadyPromise = new Promise<void>((resolve) => {
    _authReadyResolve = resolve;
  });
};

export const getTokenRef = (): TokenRef => _tokenRef;

const setAuthHeader = (config: AxiosRequestConfig, token: string | null) => {
  const headers = config.headers as Record<string, unknown> | undefined;
  if (!headers) return;
  if (token === null) {
    if (typeof (headers as { delete?: (k: string) => void }).delete === 'function') {
      (headers as { delete: (k: string) => void }).delete('Authorization');
    } else {
      delete (headers as Record<string, unknown>).Authorization;
    }
    return;
  }
  if (typeof (headers as { set?: (k: string, v: string) => void }).set === 'function') {
    (headers as { set: (k: string, v: string) => void }).set('Authorization', `Bearer ${token}`);
  } else {
    (headers as Record<string, unknown>).Authorization = `Bearer ${token}`;
  }
};

export const setupApiInterceptors = (tokenRef: TokenRef, onLogout?: LogoutHandler): void => {
  _tokenRef = tokenRef;

  if (_reqInterceptor !== null) api.interceptors.request.eject(_reqInterceptor);
  if (_resInterceptor !== null) api.interceptors.response.eject(_resInterceptor);

  _reqInterceptor = api.interceptors.request.use(
    async (config) => {
      const url = config.url || '';
      if (!_authReady && !isPublicRequest(url)) {
        try {
          await waitWithTimeout(_authReadyPromise, AUTH_READY_TIMEOUT_MS);
        } catch {
          // fall through; downstream will handle 401 via response interceptor
        }
      }

      const headers = config.headers as Record<string, unknown> | undefined;
      const existing =
        headers && typeof (headers as { get?: (k: string) => unknown }).get === 'function'
          ? (headers as { get: (k: string) => unknown }).get('Authorization')
          : (headers as Record<string, unknown> | undefined)?.Authorization;
      if (!existing && _tokenRef.current) {
        setAuthHeader(config, _tokenRef.current);
      }
      return config;
    },
    (error) => Promise.reject(error),
  );

  let isRefreshing = false;
  type Pending = { resolve: (token: string | null) => void; reject: (err: unknown) => void };
  let pendingQueue: Pending[] = [];

  const processQueue = (error: unknown, token: string | null = null) => {
    pendingQueue.forEach((p) => (error ? p.reject(error) : p.resolve(token)));
    pendingQueue = [];
  };

  _resInterceptor = api.interceptors.response.use(
    (response) => response,
    async (error) => {
      const originalRequest = error.config as RetryableConfig | undefined;

      if (error.response?.status === 403 && error.response?.data?.message) {
        message.error(error.response.data.message);
      }

      if (
        originalRequest &&
        error.response?.status === 401 &&
        !originalRequest._retry &&
        !originalRequest.url?.includes('/auth/refresh')
      ) {
        if (isRefreshing) {
          return new Promise<string | null>((resolve, reject) => {
            pendingQueue.push({ resolve, reject });
          }).then(() => {
            setAuthHeader(originalRequest, null);
            return api(originalRequest);
          });
        }

        originalRequest._retry = true;
        isRefreshing = true;

        try {
          const res = await api.post<{ access_token: string }>('/auth/refresh');
          const newToken = res.data.access_token;
          _tokenRef.current = newToken;
          processQueue(null, newToken);
          setAuthHeader(originalRequest, null);
          return api(originalRequest);
        } catch (refreshError) {
          processQueue(refreshError, null);
          onLogout?.();
          return Promise.reject(refreshError);
        } finally {
          isRefreshing = false;
        }
      }

      return Promise.reject(error);
    },
  );
};

const parseStreamError = async (response: Response): Promise<Error> => {
  try {
    const data = (await response.json()) as { error?: string; message?: string };
    return new Error(data.message || data.error || `HTTP ${response.status}`);
  } catch {
    return new Error(`HTTP ${response.status}`);
  }
};

export const streamApiEvents = (
  path: string,
  payload: unknown,
  {
    onEvent,
    onClose,
    onError,
  }: {
    onEvent: StreamEventHandler;
    onClose?: () => void;
    onError: (err: Error) => void;
  },
): AbortController => {
  const ctrl = new AbortController();

  const run = async (): Promise<void> => {
    if (!_authReady && !isPublicRequest(path)) {
      try {
        await waitWithTimeout(_authReadyPromise, AUTH_READY_TIMEOUT_MS);
      } catch {
        // fall through; backend 401 is handled as a normal stream error
      }
    }

    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (_tokenRef.current) headers.Authorization = `Bearer ${_tokenRef.current}`;

    const response = await fetch(`${api.defaults.baseURL || ''}${path}`, {
      method: 'POST',
      headers,
      credentials: 'include',
      body: JSON.stringify(payload),
      signal: ctrl.signal,
    });

    if (!response.ok) throw await parseStreamError(response);

    const reader = response.body?.getReader();
    if (!reader) throw new Error('No readable stream');

    const decoder = new TextDecoder();
    let buffer = '';

    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        onClose?.();
        return;
      }

      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split('\n\n');
      buffer = parts.pop() ?? '';

      for (const part of parts) {
        const line = part.trim();
        if (!line.startsWith('data:')) continue;
        let event: unknown;
        try {
          event = JSON.parse(line.slice(5).trim());
        } catch {
          // malformed chunk, skip
          continue;
        }
        const shouldContinue = onEvent(event);
        if (shouldContinue === false) return;
      }
    }
  };

  run().catch((err: Error) => {
    if (err.name !== 'AbortError') onError(err);
  });

  return ctrl;
};

export default api;
