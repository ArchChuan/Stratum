import { fireEvent, render, screen } from '@testing-library/react';
import type { ColumnsType, TableProps } from 'antd/es/table';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { ResponsiveDataView } from '../ResponsiveDataView';

const responsive = vi.hoisted(() => ({ isMobile: false, isCompact: false }));

vi.mock('@/shared/hooks', () => ({
  useResponsive: () => responsive,
}));

interface Row {
  id: string;
  name: string;
}

const rows: Row[] = [
  { id: 'row-1', name: '第一项' },
  { id: 'row-2', name: '第二项' },
];

const columns: ColumnsType<Row> = [
  { title: '名称', dataIndex: 'name' },
];

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: vi.fn((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
});

describe('ResponsiveDataView', () => {
  beforeEach(() => {
    responsive.isMobile = false;
  });

  it('renders the supplied rows in an Ant table on desktop', () => {
    const { container } = render(
      <ResponsiveDataView
        rows={rows}
        columns={columns}
        rowKey="id"
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    expect(container.querySelector('.ant-table')).toBeInTheDocument();
    expect(screen.getByText('第一项')).toBeInTheDocument();
    expect(container.querySelector('.mobile-card-list')).toBeNull();
  });

  it('renders mobile cards with the original row objects and action callbacks', () => {
    responsive.isMobile = true;
    const action = vi.fn();
    const renderMobileItem = vi.fn((row: Row) => (
      <button type="button" onClick={() => action(row)}>{row.name}</button>
    ));

    const { container } = render(
      <ResponsiveDataView
        rows={rows}
        columns={columns}
        rowKey={(row) => row.id}
        renderMobileItem={renderMobileItem}
      />,
    );

    expect(container.querySelector('.ant-table')).toBeNull();
    expect(container.querySelector('.mobile-card-list')).toBeInTheDocument();
    expect(renderMobileItem.mock.calls[0][0]).toBe(rows[0]);
    fireEvent.click(screen.getByRole('button', { name: '第一项' }));
    expect(action).toHaveBeenCalledWith(rows[0]);
  });

  it('preserves loading and empty semantics on mobile', () => {
    responsive.isMobile = true;
    const { container, rerender } = render(
      <ResponsiveDataView
        rows={[]}
        columns={columns}
        rowKey="id"
        loading
        emptyText="还没有条目"
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    expect(container.querySelectorAll('.ant-skeleton')).toHaveLength(3);
    expect(screen.queryByText('还没有条目')).toBeNull();

    rerender(
      <ResponsiveDataView
        rows={[]}
        columns={columns}
        rowKey="id"
        loading={false}
        emptyText="还没有条目"
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    expect(screen.getByText('还没有条目')).toBeInTheDocument();
    expect(container.querySelector('.ant-skeleton')).toBeNull();
  });

  it('uses the supplied empty text in the desktop table', () => {
    render(
      <ResponsiveDataView
        rows={[]}
        columns={columns}
        rowKey="id"
        emptyText="还没有条目"
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    expect(screen.getByText('还没有条目')).toBeInTheDocument();
  });

  it('adapts mobile pagination changes to the table onChange contract', () => {
    responsive.isMobile = true;
    const onChange = vi.fn<NonNullable<TableProps<Row>['onChange']>>();

    render(
      <ResponsiveDataView
        rows={rows}
        columns={columns}
        rowKey="id"
        pagination={{ current: 1, pageSize: 10, total: 30 }}
        onChange={onChange}
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    fireEvent.click(screen.getByTitle('2'));

    expect(onChange).toHaveBeenCalledTimes(1);
    const [pagination, filters, sorter, extra] = onChange.mock.calls[0];
    expect(pagination).toMatchObject({ current: 2, pageSize: 10, total: 30 });
    expect(filters).toEqual({});
    expect(sorter).toEqual({});
    expect(extra).toMatchObject({ action: 'paginate', currentDataSource: rows });
    expect(extra.currentDataSource[0]).toBe(rows[0]);
  });

  it('preserves the configured pagination onChange callback on mobile', () => {
    responsive.isMobile = true;
    const paginationOnChange = vi.fn();

    render(
      <ResponsiveDataView
        rows={rows}
        columns={columns}
        rowKey="id"
        pagination={{
          current: 1,
          pageSize: 10,
          total: 30,
          onChange: paginationOnChange,
        }}
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    fireEvent.click(screen.getByTitle('2'));

    expect(paginationOnChange).toHaveBeenCalledWith(2, 10);
    expect(screen.getByTitle('1')).toHaveClass('ant-pagination-item-active');
  });

  it('invokes both callback slots and strips functions from table pagination', () => {
    responsive.isMobile = true;
    const sharedCallback = vi.fn();

    render(
      <ResponsiveDataView
        rows={rows}
        columns={columns}
        rowKey="id"
        pagination={{
          current: 1,
          pageSize: 10,
          total: 30,
          onChange: sharedCallback,
          showTotal: () => '共 30 项',
        }}
        onChange={sharedCallback}
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    fireEvent.click(screen.getByTitle('2'));

    expect(sharedCallback).toHaveBeenCalledTimes(2);
    expect(sharedCallback.mock.calls[0]).toEqual([2, 10]);
    const tablePagination = sharedCallback.mock.calls[1][0];
    expect(tablePagination).toMatchObject({ current: 2, pageSize: 10, total: 30 });
    expect(tablePagination).not.toHaveProperty('onChange');
    expect(tablePagination).not.toHaveProperty('showTotal');
    expect(Object.values(tablePagination)).not.toContainEqual(expect.any(Function));
  });

  it('keeps pagination available on an empty mobile page when results exist', () => {
    responsive.isMobile = true;
    render(
      <ResponsiveDataView
        rows={[]}
        columns={columns}
        rowKey="id"
        emptyText="当前页没有条目"
        pagination={{ current: 3, pageSize: 10, total: 30 }}
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    expect(screen.getByText('当前页没有条目')).toBeInTheDocument();
    expect(screen.getByTitle('2')).toBeInTheDocument();
  });

  it('does not paginate on desktop or mobile when pagination is omitted', () => {
    const manyRows = Array.from({ length: 11 }, (_, index) => ({
      id: `row-${index}`,
      name: `条目 ${index}`,
    }));
    const view = render(
      <ResponsiveDataView
        rows={manyRows}
        columns={columns}
        rowKey="id"
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );

    expect(view.container.querySelector('.ant-pagination')).toBeNull();

    responsive.isMobile = true;
    view.rerender(
      <ResponsiveDataView
        rows={manyRows}
        columns={columns}
        rowKey="id"
        renderMobileItem={(row) => <article>{row.name}</article>}
      />,
    );
    expect(view.container.querySelector('.ant-pagination')).toBeNull();
  });
});
