import { Pagination, Table } from 'antd';
import type { TablePaginationConfig, TableProps } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Key, ReactNode } from 'react';
import { useEffect, useState } from 'react';

import { EmptyHint } from './EmptyHint';
import { ListSkeleton } from './ListSkeleton';

import { COMPACT_PAGE_SIZE } from '@/constants';
import { useResponsive } from '@/shared/hooks';

function omitFunctionValues<T extends object>(value: T): Partial<T> {
  return Object.fromEntries(
    Object.entries(value).filter(([, entry]) => typeof entry !== 'function'),
  ) as Partial<T>;
}

export interface ResponsiveDataViewProps<T extends object> {
  rows: T[];
  columns: ColumnsType<T>;
  rowKey: keyof T | ((row: T) => Key);
  loading?: boolean;
  size?: TableProps<T>['size'];
  /** Pagination is opt-in; omitting this prop disables it on every viewport. */
  pagination?: TablePaginationConfig | false;
  mobilePaginationMode?: 'client' | 'server';
  onChange?: TableProps<T>['onChange'];
  emptyText?: string;
  renderMobileItem: (row: T, index: number) => ReactNode;
}

export function ResponsiveDataView<T extends object>({
  rows,
  columns,
  rowKey,
  loading = false,
  size,
  pagination,
  mobilePaginationMode = 'client',
  onChange,
  emptyText = '暂无数据',
  renderMobileItem,
}: ResponsiveDataViewProps<T>) {
  const { isMobile } = useResponsive();
  const resolvedPagination = pagination ?? false;
  const paginationConfig = resolvedPagination === false ? undefined : resolvedPagination;
  const [mobileCurrent, setMobileCurrent] = useState(
    paginationConfig?.current ?? paginationConfig?.defaultCurrent ?? 1,
  );
  const [mobilePageSize, setMobilePageSize] = useState(
    paginationConfig?.pageSize ?? paginationConfig?.defaultPageSize ?? COMPACT_PAGE_SIZE,
  );
  const mobileTotal = paginationConfig?.total ?? rows.length;

  useEffect(() => {
    if (mobilePaginationMode !== 'client' || !paginationConfig) return;
    const lastPage = Math.max(1, Math.ceil(mobileTotal / mobilePageSize));
    setMobileCurrent((current) => Math.min(current, lastPage));
  }, [mobilePageSize, mobilePaginationMode, mobileTotal, paginationConfig]);

  if (!isMobile) {
    return (
      <Table<T>
        columns={columns}
        dataSource={rows}
        rowKey={rowKey as TableProps<T>['rowKey']}
        loading={loading}
        size={size}
        pagination={resolvedPagination}
        onChange={onChange}
        locale={{ emptyText }}
      />
    );
  }

  if (loading) return <ListSkeleton count={3} />;
  const getRowKey = (row: T): Key => (
    typeof rowKey === 'function' ? rowKey(row) : row[rowKey] as Key
  );

  const handlePageChange = (current: number, pageSize: number) => {
    if (resolvedPagination === false) return;

    if (mobilePaginationMode === 'client') {
      setMobileCurrent(current);
      setMobilePageSize(pageSize);
    }

    const paginationOnChange = resolvedPagination.onChange;
    paginationOnChange?.(current, pageSize);
    if (!onChange) return;

    onChange(
      { ...omitFunctionValues(resolvedPagination), current, pageSize },
      {},
      {},
      { action: 'paginate', currentDataSource: rows },
    );
  };

  const visibleRows = mobilePaginationMode === 'client' && paginationConfig
    ? rows.slice((mobileCurrent - 1) * mobilePageSize, mobileCurrent * mobilePageSize)
    : rows;
  const paginationCurrent = mobilePaginationMode === 'client'
    ? mobileCurrent
    : paginationConfig?.current ?? paginationConfig?.defaultCurrent;
  const paginationPageSize = mobilePaginationMode === 'client'
    ? mobilePageSize
    : paginationConfig?.pageSize ?? paginationConfig?.defaultPageSize;

  return (
    <div className="mobile-card-list">
      {visibleRows.length === 0 ? (
        <EmptyHint title={emptyText} />
      ) : visibleRows.map((row, index) => (
          <div key={getRowKey(row)}>{renderMobileItem(row, index)}</div>
        ))}
      {resolvedPagination !== false && (
        <Pagination
          {...resolvedPagination}
          responsive
          showSizeChanger={false}
          size="small"
          current={paginationCurrent}
          pageSize={paginationPageSize}
          total={mobilePaginationMode === 'client' ? mobileTotal : paginationConfig?.total}
          onChange={handlePageChange}
        />
      )}
    </div>
  );
}

export default ResponsiveDataView;
