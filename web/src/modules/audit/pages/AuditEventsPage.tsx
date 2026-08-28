import { Button, DatePicker, Form, Input, Pagination, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Dayjs } from 'dayjs';
import { useCallback } from 'react';

import { AuditEventDrawer } from '../components/AuditEventDrawer';
import { useAuditListPage, type AuditListPageFetchers } from '../hooks/useAuditListPage';
import type { ResourceChangeAudit } from '../model/audit';
import { OPERATION_LABELS } from '../model/audit';

import { RESOURCE_KIND_OPTIONS } from '@/constants';
import { EmptyHint } from '@/shared/ui';

interface FilterFormValues {
  range?: [Dayjs, Dayjs];
  actorName?: string;
  resourceKind?: string;
}

// 平台审计页复用本组件：仅替换标题/描述/空态/资源类型选项与数据源
// （fetchers 注入平台版查询函数，useAuditListPage 内部用 ref 缓存首次引用）。
// 默认值保持租户审计页行为不变。
interface AuditEventsPageProps {
  title?: string;
  description?: string;
  emptyText?: string;
  resourceKindOptions?: Array<{ value: string; label: string }>;
  fetchers?: AuditListPageFetchers;
}

export const AuditEventsPage = ({
  title = '审计日志',
  description = '租户内资源变更审计，记录 agent / skill / MCP / 知识库 / 工作流 / 评测的创建、更新、删除与生命周期操作',
  emptyText = '没有找到审计记录',
  resourceKindOptions = RESOURCE_KIND_OPTIONS,
  fetchers,
}: AuditEventsPageProps) => {
  const {
    events,
    loading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    detailId,
    detailEvent,
    detailLoading,
    applyFilters,
    handlePageChange,
    openDetail,
    closeDetail,
  } = useAuditListPage(fetchers);
  const [form] = Form.useForm<FilterFormValues>();

  const onSearch = useCallback((values: FilterFormValues) => {
    // RangePicker 输出毫秒时间戳，后端只认 RFC3339——不转换会被静默忽略。
    applyFilters({
      from: values.range?.[0]?.toISOString(),
      to: values.range?.[1]?.toISOString(),
      actorName: values.actorName?.trim() || undefined,
      resourceKind: values.resourceKind,
    });
  }, [applyFilters]);

  const onReset = useCallback(() => {
    form.resetFields();
    applyFilters({});
  }, [applyFilters, form]);

  const columns: ColumnsType<ResourceChangeAudit> = [
    {
      title: '时间',
      dataIndex: 'created_at',
      width: 160,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    { title: '操作者', dataIndex: 'actor_name', ellipsis: true, width: 140 },
    {
      title: '资源类型',
      dataIndex: 'resource_kind',
      width: 120,
      render: (v: string) => (
        <Tag color="blue">{resourceKindOptions.find((o) => o.value === v)?.label || v}</Tag>
      ),
    },
    { title: '操作', dataIndex: 'operation', width: 100, render: (v: string) => OPERATION_LABELS[v] || v },
    { title: '资源 ID', dataIndex: 'resource_id', ellipsis: true },
    {
      title: '操作',
      key: 'actions',
      width: 80,
      render: (_, record) => (
        <Button type="link" size="small" onClick={() => void openDetail(record.id)}>
          详情
        </Button>
      ),
    },
  ];

  return (
    <div>
      <div style={{ marginBottom: 16 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          {title}
        </Typography.Title>
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {description}
        </Typography.Text>
      </div>

      <Form<FilterFormValues> form={form} layout="inline" onFinish={onSearch} style={{ marginBottom: 16, rowGap: 8 }}>
        <Form.Item name="range" label="时间范围">
          <DatePicker.RangePicker showTime />
        </Form.Item>
        <Form.Item name="actorName" label="操作者">
          <Input placeholder="按姓名或登录名模糊搜索" allowClear style={{ width: 180 }} />
        </Form.Item>
        <Form.Item name="resourceKind" label="资源类型">
          <Select placeholder="全部" allowClear style={{ width: 160 }} options={resourceKindOptions} />
        </Form.Item>
        <Form.Item>
          <Space>
            <Button type="primary" htmlType="submit">
              查询
            </Button>
            <Button onClick={onReset}>重置</Button>
          </Space>
        </Form.Item>
      </Form>

      <Table<ResourceChangeAudit>
        rowKey="id"
        columns={columns}
        dataSource={events}
        loading={loading}
        size="small"
        pagination={false}
        locale={{
          emptyText: <EmptyHint title={emptyText} description="调整筛选条件后重试" />,
        }}
      />

      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
        <Pagination
          current={page}
          pageSize={pageSize}
          total={total}
          pageSizeOptions={pageSizeOptions}
          showSizeChanger
          showTotal={(t) => `共 ${t} 条记录`}
          onChange={handlePageChange}
        />
      </div>

      <AuditEventDrawer
        event={detailEvent}
        loading={detailLoading}
        open={detailId !== null}
        onClose={closeDetail}
        resourceKindOptions={resourceKindOptions}
      />
    </div>
  );
};

export default AuditEventsPage;
