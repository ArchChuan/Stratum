import { Button, Descriptions, Drawer, Form, Input, Modal, Select, Space, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useState } from 'react';

import { collaborationApi } from '../api/collaboration.api';
import type { Collaboration, TaskStep } from '../model/collaboration';

import { agentApi } from '@/modules/agent';
import { useAuth, useTenantRole } from '@/modules/iam';

interface RequestError { response?: { data?: { error?: string } } }

const STRATEGY_LABELS: Record<string, string> = {
  sequential: '顺序',
  parallel: '并行',
  swarm: '集群',
  pipeline: '流水线',
  hierarchical: '分层',
};

const STATUS_LABELS: Record<string, string> = {
  created: '已创建',
  running: '执行中',
  completed: '已完成',
  failed: '失败',
  canceled: '已取消',
};

const STATUS_COLORS: Record<string, string> = {
  created: 'default',
  running: 'blue',
  completed: 'green',
  failed: 'red',
  canceled: 'default',
};

const STEP_STATUS_LABELS: Record<string, string> = {
  pending: '待执行',
  claimed: '已认领',
  running: '执行中',
  completed: '已完成',
  failed: '失败',
  canceled: '已取消',
};

export const CollaborationsPage = () => {
  const { user } = useAuth();
  const { isAdmin } = useTenantRole();
  const [collabs, setCollabs] = useState<Collaboration[]>([]);
  const [agents, setAgents] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [detail, setDetail] = useState<Collaboration | null>(null);
  const [detailSteps, setDetailSteps] = useState<TaskStep[]>([]);
  const [detailOpen, setDetailOpen] = useState(false);
  const [startLoading, setStartLoading] = useState(false);
  const [form] = Form.useForm();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setCollabs(await collaborationApi.list());
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载协作任务失败', duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  const loadAgents = useCallback(async () => {
    try {
      setAgents((await agentApi.list()).map((a) => a.id));
    } catch {
      message.error({ content: '加载 Agent 列表失败', duration: 0 });
    }
  }, []);

  useEffect(() => {
    void load();
    void loadAgents();
  }, [load, loadAgents]);

  const canControl = useCallback(
    (collab: Collaboration) => isAdmin || collab.createdBy === user?.sub,
    [isAdmin, user],
  );

  const openCreate = useCallback(() => {
    form.resetFields();
    // 初始拉取失败时（空列表）打开弹窗自动重试，避免参与者选项永久缺失
    if (agents.length === 0) void loadAgents();
    setCreateOpen(true);
  }, [form, agents, loadAgents]);

  const handleCreate = useCallback(async () => {
    const values = await form.validateFields();
    setCreateLoading(true);
    try {
      await collaborationApi.create({
        task_description: values.taskDescription,
        strategy: values.strategy,
        participants: values.participants,
      });
      message.success({ content: '协作任务已创建', duration: 2 });
      setCreateOpen(false);
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '创建协作任务失败', duration: 0 });
    } finally {
      setCreateLoading(false);
    }
  }, [form, load]);

  const openDetail = useCallback(async (collab: Collaboration) => {
    try {
      const detailData = await collaborationApi.get(collab.id);
      setDetail(detailData.collaboration);
      setDetailSteps(detailData.steps);
      setDetailOpen(true);
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载协作详情失败', duration: 0 });
    }
  }, []);

  const closeDetail = useCallback(() => {
    setDetailOpen(false);
    setDetail(null);
    setDetailSteps([]);
  }, []);

  const handleStart = useCallback(async (collab: Collaboration) => {
    setStartLoading(true);
    try {
      await collaborationApi.start(collab.id);
      message.success({ content: '协作任务已启动', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '启动协作任务失败', duration: 0 });
    } finally {
      setStartLoading(false);
    }
  }, [load]);

  const handleCancel = useCallback((collab: Collaboration) => {
    Modal.confirm({
      title: '确认取消协作任务？',
      content: '取消后待执行步骤将不再被调度，已执行步骤保持不变。',
      onOk: async () => {
        try {
          await collaborationApi.cancel(collab.id);
          message.success({ content: '协作任务已取消', duration: 2 });
          await load();
        } catch (err) {
          message.error({ content: (err as RequestError).response?.data?.error || '取消协作任务失败', duration: 0 });
        }
      },
    });
  }, [load]);

  const columns: ColumnsType<Collaboration> = [
    { title: '任务描述', dataIndex: 'taskDescription', ellipsis: true },
    {
      title: '策略',
      dataIndex: 'strategy',
      render: (value: string) => STRATEGY_LABELS[value] ?? value,
    },
    {
      title: '状态',
      dataIndex: 'status',
      render: (value: string) => <Tag color={STATUS_COLORS[value] ?? 'default'}>{STATUS_LABELS[value] ?? value}</Tag>,
    },
    {
      title: '参与者',
      dataIndex: 'participants',
      render: (value: string[]) => (
        <Space size={4} wrap>
          {value.map((p) => <Tag key={p}>{p}</Tag>)}
        </Space>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      render: (value: string) => new Date(value).toLocaleString(),
    },
    {
      title: '操作',
      key: 'actions',
      render: (_, collab) => (
        <Space>
          <Button size="small" onClick={() => void openDetail(collab)}>详情</Button>
          {canControl(collab) && collab.status === 'created' && (
            <Button size="small" type="primary" loading={startLoading} onClick={() => void handleStart(collab)}>
              启动
            </Button>
          )}
          {canControl(collab) && (collab.status === 'created' || collab.status === 'running') && (
            <Button size="small" danger onClick={() => handleCancel(collab)}>取消</Button>
          )}
        </Space>
      ),
    },
  ];

  const stepColumns: ColumnsType<TaskStep> = [
    { title: 'Agent', dataIndex: 'agentId' },
    {
      title: '状态',
      dataIndex: 'status',
      render: (value: string) => <Tag>{STEP_STATUS_LABELS[value] ?? value}</Tag>,
    },
    {
      title: '依赖',
      dataIndex: 'dependencies',
      render: (value: string[]) => value.length ? value.join(', ') : '-',
    },
    { title: '错误', dataIndex: 'error', ellipsis: true },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button type="primary" onClick={openCreate}>创建协作任务</Button>
      </Space>
      <Table<Collaboration> rowKey="id" loading={loading} columns={columns} dataSource={collabs} />
      <Modal
        title="创建协作任务"
        open={createOpen}
        onOk={() => void handleCreate()}
        confirmLoading={createLoading}
        onCancel={() => setCreateOpen(false)}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="taskDescription" label="任务描述" rules={[{ required: true, message: '请输入任务描述' }]}>
            <Input.TextArea rows={3} maxLength={2000} showCount />
          </Form.Item>
          <Form.Item name="strategy" label="协作策略" rules={[{ required: true, message: '请选择策略' }]}>
            <Select
              options={Object.entries(STRATEGY_LABELS).map(([value, label]) => ({ value, label }))}
            />
          </Form.Item>
          <Form.Item name="participants" label="参与者" rules={[{ required: true, message: '请选择参与者' }]}>
            <Select
              mode="multiple"
              showSearch
              options={agents.map((a) => ({ value: a, label: a }))}
              placeholder="选择参与协作的 Agent"
              filterOption={(input, option) =>
                (option?.label as string).toLowerCase().includes(input.toLowerCase())
              }
            />
          </Form.Item>
        </Form>
      </Modal>
      <Drawer
        title={detail ? `协作任务 ${detail.id}` : '协作任务详情'}
        open={detailOpen}
        onClose={closeDetail}
        width={640}
      >
        {detail && (
          <>
            <Descriptions column={1} bordered size="small">
              <Descriptions.Item label="任务描述">{detail.taskDescription}</Descriptions.Item>
              <Descriptions.Item label="策略">{STRATEGY_LABELS[detail.strategy] ?? detail.strategy}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={STATUS_COLORS[detail.status] ?? 'default'}>{STATUS_LABELS[detail.status] ?? detail.status}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="创建人">{detail.createdBy}</Descriptions.Item>
              <Descriptions.Item label="参与者">
                <Space size={4} wrap>
                  {detail.participants.map((p) => <Tag key={p}>{p}</Tag>)}
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="创建时间">{new Date(detail.createdAt).toLocaleString()}</Descriptions.Item>
              {detail.startedAt && (
                <Descriptions.Item label="开始时间">{new Date(detail.startedAt).toLocaleString()}</Descriptions.Item>
              )}
              {detail.completedAt && (
                <Descriptions.Item label="结束时间">{new Date(detail.completedAt).toLocaleString()}</Descriptions.Item>
              )}
            </Descriptions>
            <Table<TaskStep> rowKey="id" size="small" columns={stepColumns} dataSource={detailSteps}
              pagination={false} style={{ marginTop: 16 }} />
          </>
        )}
      </Drawer>
    </div>
  );
};
