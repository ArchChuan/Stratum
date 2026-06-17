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
