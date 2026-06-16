import axios from 'axios';
import { message } from 'antd';
import { API_DEFAULT_TIMEOUT_MS } from '../constants';

const api = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '',
  timeout: API_DEFAULT_TIMEOUT_MS,
  withCredentials: true,
});

let _tokenRef = { current: null };
let _reqInterceptor = null;
let _resInterceptor = null;

export const getTokenRef = () => _tokenRef;

export const setupApiInterceptors = (tokenRef, onLogout) => {
  _tokenRef = tokenRef;

  if (_reqInterceptor !== null) {
    api.interceptors.request.eject(_reqInterceptor);
  }
  if (_resInterceptor !== null) {
    api.interceptors.response.eject(_resInterceptor);
  }

  _reqInterceptor = api.interceptors.request.use(
    (config) => {
      const hasAuth = config.headers.get
        ? config.headers.get('Authorization')
        : config.headers['Authorization'];
      if (!hasAuth) {
        const token = _tokenRef.current || localStorage.getItem('access_token');
        if (token) {
          config.headers['Authorization'] = `Bearer ${token}`;
        }
      }
      return config;
    },
    (error) => Promise.reject(error)
  );

  let isRefreshing = false;
  let pendingQueue = [];

  const processQueue = (error, token = null) => {
    pendingQueue.forEach((p) => (error ? p.reject(error) : p.resolve(token)));
    pendingQueue = [];
  };

  _resInterceptor = api.interceptors.response.use(
    (response) => response,
    async (error) => {
      const originalRequest = error.config;

      if (
        error.response?.status === 403 &&
        error.response?.data?.message
      ) {
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
            originalRequest.headers.set
              ? originalRequest.headers.set('Authorization', null)
              : delete originalRequest.headers['Authorization'];
            return api(originalRequest);
          });
        }

        originalRequest._retry = true;
        isRefreshing = true;

        try {
          const res = await api.post('/auth/refresh');
          const newToken = res.data.access_token;
          _tokenRef.current = newToken;
          localStorage.setItem('access_token', newToken);
          processQueue(null, newToken);
          originalRequest.headers.set
            ? originalRequest.headers.set('Authorization', null)
            : delete originalRequest.headers['Authorization'];
          return api(originalRequest);
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
    }
  );
};

export default api;
