import axios, { type AxiosInstance, type InternalAxiosRequestConfig } from 'axios';
import { message } from 'antd';
import { API_DEFAULT_TIMEOUT_MS } from '@/constants';

type TokenRef = { current: string | null };

export const http: AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '',
  timeout: API_DEFAULT_TIMEOUT_MS,
  withCredentials: true,
});

let tokenRef: TokenRef = { current: null };
let reqInterceptor: number | null = null;
let resInterceptor: number | null = null;

export const getTokenRef = (): TokenRef => tokenRef;

type RetryConfig = InternalAxiosRequestConfig & { _retry?: boolean };

export const setupApiInterceptors = (
  ref: TokenRef,
  onLogout?: () => void,
): void => {
  tokenRef = ref;

  if (reqInterceptor !== null) http.interceptors.request.eject(reqInterceptor);
  if (resInterceptor !== null) http.interceptors.response.eject(resInterceptor);

  reqInterceptor = http.interceptors.request.use(
    (config) => {
      const headers = config.headers as unknown as {
        get?: (k: string) => string | null;
        Authorization?: string;
        [k: string]: unknown;
      };
      const hasAuth = headers.get
        ? headers.get('Authorization')
        : headers['Authorization'];
      if (!hasAuth) {
        const token = tokenRef.current || localStorage.getItem('access_token');
        if (token) {
          (config.headers as Record<string, unknown>)['Authorization'] = `Bearer ${token}`;
        }
      }
      return config;
    },
    (error) => Promise.reject(error),
  );

  let isRefreshing = false;
  let pendingQueue: Array<{ resolve: (v?: unknown) => void; reject: (e: unknown) => void }> = [];
  const processQueue = (error: unknown, token: string | null = null) => {
    pendingQueue.forEach((p) => (error ? p.reject(error) : p.resolve(token)));
    pendingQueue = [];
  };

  resInterceptor = http.interceptors.response.use(
    (response) => response,
    async (error) => {
      const originalRequest = error.config as RetryConfig | undefined;
      if (!originalRequest) return Promise.reject(error);

      if (error.response?.status === 403 && error.response?.data?.message) {
        message.error(error.response.data.message);
      }

      if (
        error.response?.status === 401 &&
        !originalRequest._retry &&
        !originalRequest.url?.includes('/auth/refresh')
      ) {
        if (isRefreshing) {
          return new Promise((resolve, reject) => {
            pendingQueue.push({ resolve, reject });
          }).then(() => {
            const headers = originalRequest.headers as Record<string, unknown> & {
              set?: (k: string, v: unknown) => void;
            };
            if (headers.set) headers.set('Authorization', null);
            else delete headers['Authorization'];
            return http(originalRequest);
          });
        }

        originalRequest._retry = true;
        isRefreshing = true;

        try {
          const res = await http.post('/auth/refresh');
          const newToken = res.data.access_token as string;
          tokenRef.current = newToken;
          localStorage.setItem('access_token', newToken);
          processQueue(null, newToken);
          const headers = originalRequest.headers as Record<string, unknown> & {
            set?: (k: string, v: unknown) => void;
          };
          if (headers.set) headers.set('Authorization', null);
          else delete headers['Authorization'];
          return http(originalRequest);
        } catch (refreshError) {
          processQueue(refreshError, null);
          localStorage.removeItem('access_token');
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

export default http;
