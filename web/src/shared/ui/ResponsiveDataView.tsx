import { Pagination, Table } from 'antd';
import type { TablePaginationConfig, TableProps } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Key, ReactNode } from 'react';

import { EmptyHint } from './EmptyHint';
import { ListSkeleton } from './ListSkeleton';

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
  /** Pagination is opt-in; omitting this prop disables it on every viewport. */
  pagination?: TablePaginationConfig | false;
  onChange?: TableProps<T>['onChange'];
  emptyText?: string;
  renderMobileItem: (row: T, index: number) => ReactNode;
}

export function ResponsiveDataView<T extends object>({
  rows,
  columns,
  rowKey,
  loading = false,
  pagination,
  onChange,
  emptyText = '暂无数据',
  renderMobileItem,
}: ResponsiveDataViewProps<T>) {
  const { isMobile } = useResponsive();
  const resolvedPagination = pagination ?? false;

  if (!isMobile) {
    return (
      <Table<T>
        columns={columns}
        dataSource={rows}
        rowKey={rowKey as TableProps<T>['rowKey']}
        loading={loading}
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

  return (
    <div className="mobile-card-list">
      {rows.length === 0 ? (
        <EmptyHint title={emptyText} />
      ) : rows.map((row, index) => (
          <div key={getRowKey(row)}>{renderMobileItem(row, index)}</div>
        ))}
      {resolvedPagination !== false && (
        <Pagination
          {...resolvedPagination}
          responsive
          showSizeChanger={false}
          size="small"
          onChange={handlePageChange}
        />
      )}
    </div>
  );
}

export default ResponsiveDataView;
