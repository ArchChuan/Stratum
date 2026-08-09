import type { AxiosError } from 'axios';

type ApiErrorPayload = { error?: string; message?: string };

export const extractErrorMessage = (err: unknown, fallback = '操作失败'): string => {
  const axiosErr = err as AxiosError<ApiErrorPayload> | undefined;
  return (
    axiosErr?.response?.data?.error ||
    axiosErr?.response?.data?.message ||
    (err instanceof Error ? err.message : '') ||
    fallback
  );
};

// 403 判定统一入口（权限变更场景页面刻意静默，集中判定避免各处手写）
export const isForbidden = (err: unknown): boolean =>
  (err as AxiosError)?.response?.status === 403;
