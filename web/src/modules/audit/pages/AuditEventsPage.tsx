import { Button, DatePicker, Form, Input, Pagination, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { Dayjs } from 'dayjs';
import { useCallback } from 'react';

import { AuditEventDrawer } from '../components/AuditEventDrawer';
import { useAuditListPage } from '../hooks/useAuditListPage';
import type { AuditEvent } from '../model/audit';
import { OUTCOME_COLORS, OUTCOME_LABELS, RISK_LEVEL_COLORS, RISK_LEVEL_LABELS } from '../model/audit';

import { EmptyHint } from '@/shared/ui';

interface FilterFormValues {
  range?: [Dayjs, Dayjs];
  action?: string;
  risk_level?: string;
  outcome?: string;
  resource_type?: string;
}

export const AuditEventsPage = () => {
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
  } = useAuditListPage();
  const [form] = Form.useForm<FilterFormValues>();

  const onSearch = useCallback((values: FilterFormValues) => {
    // RangePicker 输出毫秒时间戳，后端只认 RFC3339——不转换会被静默忽略。
    applyFilters({
      from: values.range?.[0]?.toISOString(),
      to: values.range?.[1]?.toISOString(),
      action: values.action?.trim() || undefined,
      risk_level: values.risk_level,
      outcome: values.outcome,
      resource_type: values.resource_type?.trim() || undefined,
    });
  }, [applyFilters]);

  const onReset = useCallback(() => {
    form.resetFields();
    applyFilters({});
  }, [applyFilters, form]);

  const columns: ColumnsType<AuditEvent> = [
    {
      title: '时间',
      dataIndex: 'occurred_at',
      width: 160,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    { title: '操作者', dataIndex: ['actor', 'actor_id'], ellipsis: true, width: 140 },
    { title: '操作', dataIndex: 'action', ellipsis: true },
    {
      title: '风险·结果',
      key: 'risk_outcome',
      width: 120,
      render: (_, record) => (
        <Space size={4}>
          <Tag color={RISK_LEVEL_COLORS[record.risk_level]}>{RISK_LEVEL_LABELS[record.risk_level] || record.risk_level}</Tag>
          <Tag color={OUTCOME_COLORS[record.outcome]}>{OUTCOME_LABELS[record.outcome] || record.outcome}</Tag>
        </Space>
      ),
    },
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
          审计日志
        </Typography.Title>
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          租户内操作审计事件，高风险变更留存变更前后快照
        </Typography.Text>
      </div>

      <Form<FilterFormValues> form={form} layout="inline" onFinish={onSearch} style={{ marginBottom: 16, rowGap: 8 }}>
        <Form.Item name="range" label="时间范围">
          <DatePicker.RangePicker showTime />
        </Form.Item>
        <Form.Item name="action" label="操作">
          <Input placeholder="如 POST /v1/agents" allowClear style={{ width: 180 }} />
        </Form.Item>
        <Form.Item name="risk_level" label="风险">
          <Select
            placeholder="全部"
            allowClear
            style={{ width: 96 }}
            options={[
              { value: 'low', label: '低' },
              { value: 'medium', label: '中' },
              { value: 'high', label: '高' },
            ]}
          />
        </Form.Item>
        <Form.Item name="outcome" label="结果">
          <Select
            placeholder="全部"
            allowClear
            style={{ width: 96 }}
            options={[
              { value: 'success', label: '成功' },
              { value: 'error', label: '失败' },
            ]}
          />
        </Form.Item>
        <Form.Item name="resource_type" label="资源类型">
          <Input placeholder="如 http_request" allowClear style={{ width: 140 }} />
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

      <Table<AuditEvent>
        rowKey="id"
        columns={columns}
        dataSource={events}
        loading={loading}
        size="small"
        pagination={false}
        locale={{
          emptyText: <EmptyHint title="没有找到审计记录" description="调整筛选条件后重试" />,
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

      <AuditEventDrawer event={detailEvent} loading={detailLoading} open={detailId !== null} onClose={closeDetail} />
    </div>
  );
};

export default AuditEventsPage;
