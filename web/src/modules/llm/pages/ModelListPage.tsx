import {
  DeleteOutlined,
  ReloadOutlined,
  RobotOutlined,
} from '@ant-design/icons';
import {
  Button,
  Card,
  Empty,
  message,
  Modal,
  Select,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import { useCallback, useMemo, useState } from 'react';

import { ModelCapabilityTags } from '../components/ModelCapabilityTags';
import { ModelEditDrawer } from '../components/ModelEditDrawer';
import { useModels } from '../hooks/useModels';
import type { Model, ModelCapability, UpdateModelInput } from '../model/llm';

import { LLM_DEFAULT_PAGE_SIZE } from '@/constants';
import { useTenantRole } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

const { Text } = Typography;

const CAP_FILTER_OPTIONS: { label: string; value: ModelCapability }[] = [
  { label: '对话', value: 'chat' },
  { label: '嵌入', value: 'embedding' },
  { label: '视觉', value: 'vision' },
  { label: '工具调用', value: 'tool_use' },
  { label: '推理', value: 'reasoning' },
];

export function ModelListPage() {
  const { models, loading, refresh, toggleModel, updateModel, deleteModel, setDefaultEmbedding } =
    useModels();
  const { isAdmin } = useTenantRole();
  const [capFilter, setCapFilter] = useState<ModelCapability | undefined>();
  const [editModel, setEditModel] = useState<Model | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [editLoading, setEditLoading] = useState(false);
  const [defaultLoading, setDefaultLoading] = useState<string | null>(null);

  const filtered = useMemo(
    () =>
      capFilter ? models.filter((m) => m.capabilities.includes(capFilter)) : models,
    [models, capFilter],
  );

  const handleEdit = useCallback((record: Model) => {
    setEditModel(record);
    setEditOpen(true);
  }, []);

  const handleEditSubmit = useCallback(
    async (id: string, values: UpdateModelInput) => {
      setEditLoading(true);
      try {
        await updateModel(id, values);
        message.success({ content: '模型已更新', duration: 2 });
        setEditOpen(false);
      } catch (err: unknown) {
        message.error({ content: extractErrorMessage(err, '更新模型失败'), duration: 0 });
      } finally {
        setEditLoading(false);
      }
    },
    [updateModel],
  );

  const handleSetDefault = useCallback(
    async (record: Model) => {
      setDefaultLoading(record.id);
      try {
        await setDefaultEmbedding(record.id, !record.defaultEmbedding);
      } finally {
        setDefaultLoading(null);
      }
    },
    [setDefaultEmbedding],
  );

  const handleDelete = useCallback(
    (record: Model) => {
      Modal.confirm({
        title: '确认删除',
        content: `确定要删除模型 "${record.displayName || record.name}" 吗？`,
        okText: '删除',
        okType: 'danger',
        cancelText: '取消',
        onOk: async () => {
          try {
            await deleteModel(record.id);
          } catch {
            // error handled in hook
          }
        },
      });
    },
    [deleteModel],
  );

  const columns = [
    {
      title: '显示名称',
      dataIndex: 'displayName',
      key: 'displayName',
      width: 160,
      render: (v: string, record: Model) => (
        <span>
          <Text>{v || '-'}</Text>
          {record.recommended && (
            <Text type="success" style={{ marginLeft: 6, fontSize: 12 }}>
              推荐
            </Text>
          )}
          {record.defaultEmbedding && (
            <Tag color="purple" style={{ marginLeft: 6, fontSize: 12 }}>
              默认嵌入
            </Tag>
          )}
        </span>
      ),
    },
    {
      title: 'API 名称',
      dataIndex: 'name',
      key: 'name',
      width: 200,
      render: (v: string) => <Text code>{v}</Text>,
    },
    {
      title: '厂商 ID',
      dataIndex: 'providerId',
      key: 'providerId',
      width: 100,
      ellipsis: true,
    },
    {
      title: '能力',
      dataIndex: 'capabilities',
      key: 'capabilities',
      width: 240,
      render: (caps: ModelCapability[]) => <ModelCapabilityTags capabilities={caps} />,
    },
    {
      title: '上下文',
      dataIndex: 'contextWindow',
      key: 'contextWindow',
      width: 100,
      render: (v: number) => (v ? v.toLocaleString() : '-'),
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      key: 'enabled',
      width: 80,
      render: (_enabled: boolean, record: Model) => (
        <Switch
          size="small"
          checked={_enabled}
          disabled={!isAdmin}
          onChange={(checked) => toggleModel(record.id, checked)}
        />
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 200,
      render: (_: unknown, record: Model) => (
        <span style={{ display: 'flex', gap: 8 }}>
          <Button size="small" onClick={() => handleEdit(record)} disabled={!isAdmin}>
            编辑
          </Button>
          {isAdmin &&
            record.capabilities.includes('embedding') &&
            record.enabled && (
              <Button
                size="small"
                type={record.defaultEmbedding ? 'default' : 'primary'}
                loading={defaultLoading === record.id}
                onClick={() => void handleSetDefault(record)}
              >
                {record.defaultEmbedding ? '取消默认' : '设为默认'}
              </Button>
            )}
          {isAdmin && (
            <Button
              size="small"
              danger
              icon={<DeleteOutlined />}
              onClick={() => handleDelete(record)}
            />
          )}
        </span>
      ),
    },
  ];

  return (
    <Card
      title="模型目录"
      extra={
        <span style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <Select
            allowClear
            placeholder="按能力筛选"
            options={CAP_FILTER_OPTIONS}
            value={capFilter}
            onChange={(v) => setCapFilter(v)}
            style={{ width: 140 }}
          />
          <Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>
            刷新
          </Button>
        </span>
      }
    >
      {filtered.length === 0 && !loading ? (
        <Empty
          image={<RobotOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description={
            capFilter
              ? '没有找到匹配该能力的模型'
              : '还没有模型，请先在厂商管理中执行发现模型'
          }
          style={{ padding: '60px 0' }}
        />
      ) : (
        <Table
          dataSource={filtered}
          columns={columns}
          rowKey="id"
          loading={loading}
          pagination={{ pageSize: LLM_DEFAULT_PAGE_SIZE, showSizeChanger: true, showTotal: (t) => `共 ${t} 个` }}
        />
      )}

      <ModelEditDrawer
        open={editOpen}
        model={editModel}
        onClose={() => {
          setEditOpen(false);
          setEditModel(null);
        }}
        onSubmit={handleEditSubmit}
        loading={editLoading}
      />
    </Card>
  );
}
