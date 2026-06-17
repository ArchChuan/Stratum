import { useState, useCallback } from 'react';

import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '@/constants';

export interface PaginationState {
  current: number;
  pageSize: number;
  total: number;
}

export const usePagination = (initial?: Partial<PaginationState>) => {
  const [state, setState] = useState<PaginationState>({
    current: 1,
    pageSize: DEFAULT_PAGE_SIZE,
    total: 0,
    ...initial,
  });

  const onChange = useCallback((current: number, pageSize: number) => {
    setState((prev) => ({ ...prev, current, pageSize }));
  }, []);

  const setTotal = useCallback((total: number) => {
    setState((prev) => ({ ...prev, total }));
  }, []);

  return {
    ...state,
    setTotal,
    pageSizeOptions: PAGE_SIZE_OPTIONS,
    tableProps: {
      pagination: {
        current: state.current,
        pageSize: state.pageSize,
        total: state.total,
        pageSizeOptions: PAGE_SIZE_OPTIONS,
        showSizeChanger: true,
        onChange,
      },
    },
  };
};
