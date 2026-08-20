import {
  DeleteOutlined,
  PlusOutlined,
  ReloadOutlined,
  ScheduleOutlined,
} from '@ant-design/icons';
import {
  Button,
  Card,
  Empty,
  Form,
  Modal,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useState } from 'react';

import { ScheduledTaskFormModal, type ScheduledTaskFormValues } from '../components/ScheduledTaskFormModal';
import { useScheduledTasks } from '../hooks/useScheduledTasks';
import type { ScheduledTask } from '../model/scheduledTask';

import { useTenantRole } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

const { Text, Title } = Typography;

const RUN_STATUS_LABELS: Record<string, string> = {
  ok: '成功',
  error: '失败',
  '': '未运行',
};

const RUN_STATUS_COLORS: Record<string, string> = {
  ok: 'green',
  error: 'red',
  '': 'default',
};

export function ScheduledTaskListPage() {
  const {
    tasks,
    total,
    page,
    pageSize,
    loading,
    createLoading,
    refresh,
    changePage,
    createTask,
    updateTask,
    deleteTask,
    setEnabled,
  } = useScheduledTasks();
  const { isAdmin } = useTenantRole();
  const [form] = Form.useForm<ScheduledTaskFormValues>();
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editing, setEditing] = useState<ScheduledTask | null>(null);

  const handleCreate = useCallback(async (values: ScheduledTaskFormValues) => {
    try {
      await createTask({
        name: values.name.trim(),
        workflowId: values.workflowId,
        versionId: values.versionId,
        inputTemplate: JSON.parse(values.inputTemplate) as Record<string, unknown>,
        cronExpr: values.cronExpr.trim(),
      });
      setCreateOpen(false);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '创建定时任务失败'), duration: 3 });
    }
  }, [createTask]);

  const handleUpdate = useCallback(async (values: ScheduledTaskFormValues) => {
    if (!editing) return;
    try {
      await updateTask(editing.id, {
        name: values.name.trim(),
        workflowId: values.workflowId,
        versionId: values.versionId,
        inputTemplate: JSON.parse(values.inputTemplate) as Record<string, unknown>,
        cronExpr: values.cronExpr.trim(),
      });
      setEditOpen(false);
      setEditing(null);
    } catch (err) {
      message.error({ content: extractErrorMessage(err, '更新定时任务失败'), duration: 3 });
    }
  }, [editing, updateTask]);

  const openEdit = useCallback((record: ScheduledTask) => {
    setEditing(record);
    setEditOpen(true);
  }, []);

  const handleDelete = useCallback((record: ScheduledTask) => {
    Modal.confirm({
      title: '确认删除',
      content: `确定要删除定时任务 "${record.name}" 吗？删除后不可恢复。`,
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        try {
          await deleteTask(record.id);
        } catch {
          // error handled in hook
        }
      },
    });
  }, [deleteTask]);

  const columns: ColumnsType<ScheduledTask> = [
    { title: '名称', dataIndex: 'name', key: 'name', ellipsis: true },
    { title: '工作流', dataIndex: 'workflowId', key: 'workflowId', ellipsis: true },
    { title: '版本', dataIndex: 'versionId', key: 'versionId', ellipsis: true, width: 140 },
    { title: 'Cron', dataIndex: 'cronExpr', key: 'cronExpr', width: 140, render: (v: string) => <Text code>{v}</Text> },
    {
      title: '下次触发',
      dataIndex: 'nextFireAt',
      key: 'nextFireAt',
      width: 170,
      render: (v: string) => new Date(v).toLocaleString(),
    },
    {
      title: '上次运行',
      key: 'lastRun',
      width: 190,
      render: (_: unknown, record: ScheduledTask) => {
        if (!record.lastRunAt) return <Text type="secondary">-</Text>;
        return (
          <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
            <Text type="secondary">{new Date(record.lastRunAt).toLocaleString()}</Text>
            <Tag color={RUN_STATUS_COLORS[record.lastRunStatus] || 'default'}>
              {RUN_STATUS_LABELS[record.lastRunStatus] || record.lastRunStatus}
            </Tag>
          </span>
        );
      },
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      key: 'enabled',
      width: 80,
      render: (enabled: boolean, record: ScheduledTask) => (
        <Switch
          size="small"
          checked={enabled}
          disabled={!isAdmin}
          onChange={(checked) => setEnabled(record.id, checked)}
        />
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 120,
      render: (_: unknown, record: ScheduledTask) => (
        <span style={{ display: 'flex', gap: 8 }}>
          <Button size="small" disabled={!isAdmin} onClick={() => openEdit(record)}>
            编辑
          </Button>
          {isAdmin && (
            <Button
              size="small"
              danger
              icon={<DeleteOutlined />}
              aria-label={`删除定时任务 ${record.name}`}
              onClick={() => handleDelete(record)}
            />
          )}
        </span>
      ),
    },
  ];

  return (
    <div style={{ padding: 24 }}>
      <Title level={4} style={{ marginBottom: 20 }}>
        定时任务
      </Title>
      <Card
      extra={
        <span style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <Button icon={<ReloadOutlined />} onClick={() => void refresh()} loading={loading}>
            刷新
          </Button>
          {isAdmin && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              新建定时任务
            </Button>
          )}
        </span>
      }
    >
      {tasks.length === 0 && !loading ? (
        <Empty
          image={<ScheduleOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description={isAdmin ? '还没有定时任务，点击右上角创建' : '暂无定时任务'}
          style={{ padding: '60px 0' }}
        />
      ) : (
        <Table
          dataSource={tasks}
          columns={columns}
          rowKey="id"
          loading={loading}
          pagination={{
            current: page,
            pageSize,
            total,
            showSizeChanger: true,
            showTotal: (t) => `共 ${t} 条`,
            onChange: changePage,
          }}
        />
      )}
      <ScheduledTaskFormModal
        open={createOpen}
        loading={createLoading}
        form={form}
        editing={null}
        onClose={() => setCreateOpen(false)}
        onSubmit={handleCreate}
      />
      <ScheduledTaskFormModal
        open={editOpen}
        loading={createLoading}
        form={form}
        editing={editing}
        onClose={() => {
          setEditOpen(false);
          setEditing(null);
        }}
        onSubmit={handleUpdate}
      />
      </Card>
    </div>
  );
}
