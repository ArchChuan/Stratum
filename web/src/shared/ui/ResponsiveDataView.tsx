import { Pagination, Table } from 'antd';
import type { TablePaginationConfig, TableProps } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Key, ReactNode } from 'react';

import { EmptyHint } from './EmptyHint';
import { ListSkeleton } from './ListSkeleton';

import { useResponsive } from '@/shared/hooks';

export interface ResponsiveDataViewProps<T extends object> {
  rows: T[];
  columns: ColumnsType<T>;
  rowKey: keyof T | ((row: T) => Key);
  loading?: boolean;
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

  if (!isMobile) {
    return (
      <Table<T>
        columns={columns}
        dataSource={rows}
        rowKey={rowKey as TableProps<T>['rowKey']}
        loading={loading}
        pagination={pagination}
        onChange={onChange}
        locale={{ emptyText }}
      />
    );
  }

  if (loading) return <ListSkeleton count={3} />;
  if (rows.length === 0) return <EmptyHint title={emptyText} />;

  const getRowKey = (row: T): Key => (
    typeof rowKey === 'function' ? rowKey(row) : row[rowKey] as Key
  );

  const handlePageChange = (current: number, pageSize: number) => {
    if (!onChange || pagination === false) return;

    onChange(
      { ...pagination, current, pageSize },
      {},
      {},
      { action: 'paginate', currentDataSource: rows },
    );
  };

  return (
    <div className="mobile-card-list">
      {rows.map((row, index) => (
        <div key={getRowKey(row)}>{renderMobileItem(row, index)}</div>
      ))}
      {pagination !== false && pagination && (
        <Pagination
          {...pagination}
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
