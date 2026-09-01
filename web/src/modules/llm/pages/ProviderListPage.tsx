import {
  ApiOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import {
  Button,
  Card,
  Empty,
  message,
  Modal,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { useCallback, useState } from 'react';

import { llmApi } from '../api/llm.api';
import { AddModelModal } from '../components/AddModelModal';
import type { AddModelFormValues } from '../components/AddModelModal';
import { DiscoverResultModal } from '../components/DiscoverResultModal';
import { ProviderForm } from '../components/ProviderForm';
import type { ProviderFormValues } from '../components/ProviderForm';
import { useProviders } from '../hooks/useProviders';
import type { CreateProviderInput, Model, Provider, ProviderKind } from '../model/llm';

import { usePlatformAdminCanEdit } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

const { Text } = Typography;

const KIND_LABELS: Record<ProviderKind, string> = {
  openai_compat: 'OpenAI 兼容',
  anthropic: 'Anthropic',
  ollama: 'Ollama',
};

const KIND_COLORS: Record<ProviderKind, string> = {
  openai_compat: 'blue',
  anthropic: 'green',
  ollama: 'orange',
};

interface Props {
  /** 手动添加模型成功后回调，用于触发模型目录刷新。 */
  onModelCreated?: () => void;
}

export function ProviderListPage({ onModelCreated }: Props) {
  const { providers, loading, createLoading, updateLoading, refresh, createProvider, updateProvider, deleteProvider } = useProviders();
  const canEdit = usePlatformAdminCanEdit();
  const [createOpen, setCreateOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<Provider | null>(null);
  const [discoveringIds, setDiscoveringIds] = useState<Set<string>>(new Set());
  const [discoverResults, setDiscoverResults] = useState<Model[]>([]);
  const [discoverProviderName, setDiscoverProviderName] = useState('');
  const [discoverOpen, setDiscoverOpen] = useState(false);
  const [addModelOpen, setAddModelOpen] = useState(false);
  const [addModelProvider, setAddModelProvider] = useState<Provider | null>(null);
  const [addModelLoading, setAddModelLoading] = useState(false);

  const handleCreate = useCallback(
    async (values: CreateProviderInput) => {
      await createProvider(values);
      setCreateOpen(false);
    },
    [createProvider],
  );

  const handleEdit = useCallback((record: Provider) => {
    setEditingProvider(record);
    setEditOpen(true);
  }, []);

  const handleEditSubmit = useCallback(
    async (values: ProviderFormValues) => {
      if (!editingProvider) return;
      // Send all fields; backend treats empty apiKey as "keep existing".
      await updateProvider(editingProvider.id, {
        name: values.name,
        kind: values.kind,
        baseUrl: values.baseUrl,
        apiKey: values.apiKey,
        defaultModel: values.defaultModel,
      });
      setEditOpen(false);
      setEditingProvider(null);
    },
    [editingProvider, updateProvider],
  );

  const handleDelete = useCallback(
    (record: Provider) => {
      Modal.confirm({
        title: '确认删除',
        content: `确定要删除厂商 "${record.name}" 吗？其管理的模型也将一并删除。`,
        okText: '删除',
        okType: 'danger',
        cancelText: '取消',
        onOk: async () => {
          try {
            await deleteProvider(record.id);
          } catch {
            // error handled in hook
          }
        },
      });
    },
    [deleteProvider],
  );

  const handleDiscover = useCallback(async (record: Provider) => {
    setDiscoveringIds(prev => new Set(prev).add(record.id));
    try {
      const res = await llmApi.discoverModels(record.id);
      setDiscoverResults(res.models);
      setDiscoverProviderName(record.name);
      setDiscoverOpen(true);
    } catch (err) {
      Modal.error({
        title: '发现模型失败',
        content: extractErrorMessage(err) || '请检查厂商配置和网络连接',
      });
    } finally {
      setDiscoveringIds(prev => { const next = new Set(prev); next.delete(record.id); return next; });
    }
  }, []);

  const handleAddModel = useCallback(
    async (values: AddModelFormValues) => {
      if (!addModelProvider) return;
      setAddModelLoading(true);
      try {
        await llmApi.createModel({
          providerId: addModelProvider.id,
          name: values.name.trim(),
          capabilities: values.capabilities,
          // 0 = 未设置，后端回退到厂商默认。
          contextWindow: values.contextWindow ?? 0,
          maxTokens: values.maxTokens ?? 0,
        });
        message.success({ content: '模型已添加', duration: 2 });
        setAddModelOpen(false);
        setAddModelProvider(null);
        onModelCreated?.();
      } catch (err) {
        message.error({ content: extractErrorMessage(err, '添加模型失败'), duration: 3 });
      } finally {
        setAddModelLoading(false);
      }
    },
    [addModelProvider, onModelCreated],
  );

  const handleHealthCheck = useCallback(async (record: Provider) => {
    try {
      const res = await llmApi.healthCheck(record.id);
      Modal.success({
        title: '健康检查',
        content: `厂商 "${record.name}" ${res.status === 'healthy' ? '连接正常' : '状态: ' + res.status}`,
      });
    } catch (err) {
      Modal.error({
        title: '健康检查失败',
        content: extractErrorMessage(err) || '无法连接到厂商',
      });
    }
  }, []);

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      width: 180,
    },
    {
      title: '类型',
      dataIndex: 'kind',
      key: 'kind',
      width: 140,
      render: (kind: ProviderKind) => (
        <Tag color={KIND_COLORS[kind]}>{KIND_LABELS[kind]}</Tag>
      ),
    },
    {
      title: 'Base URL',
      dataIndex: 'baseUrl',
      key: 'baseUrl',
      ellipsis: true,
      render: (url: string) => (
        <Tooltip title={url}>
          <Text copyable={{ text: url }} style={{ fontSize: 13 }}>
            {url}
          </Text>
        </Tooltip>
      ),
    },
    {
      title: '默认模型',
      dataIndex: 'defaultModel',
      key: 'defaultModel',
      width: 160,
      render: (v: string) => v || '-',
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      key: 'enabled',
      width: 80,
      render: (_enabled: boolean) => (
        <Tag color={_enabled ? 'green' : 'default'}>{_enabled ? '启用' : '停用'}</Tag>
      ),
    },
    {
      title: '操作',
      key: 'actions',
      width: 360,
      render: (_: unknown, record: Provider) => (
        <span style={{ display: 'flex', gap: 8 }}>
          <Button
            size="small"
            disabled={!canEdit}
            onClick={() => {
              setAddModelProvider(record);
              setAddModelOpen(true);
            }}
          >
            添加模型
          </Button>
          <Button
            size="small"
            disabled={!canEdit}
            onClick={() => handleDiscover(record)}
            loading={discoveringIds.has(record.id)}
          >
            发现模型
          </Button>
          <Button size="small" disabled={!canEdit} onClick={() => handleHealthCheck(record)}>
            健康检查
          </Button>
          <Tooltip title="编辑">
            <Button
              size="small"
              aria-label="编辑"
              disabled={!canEdit}
              icon={<EditOutlined />}
              onClick={() => handleEdit(record)}
            />
          </Tooltip>
          <Tooltip title="删除">
            <Button
              size="small"
              aria-label="删除"
              disabled={!canEdit}
              danger
              icon={<DeleteOutlined />}
              onClick={() => handleDelete(record)}
            />
          </Tooltip>
        </span>
      ),
    },
  ];

  return (
    <Card
      title="厂商管理"
      extra={
        <span style={{ display: 'flex', gap: 8 }}>
          <Button icon={<ReloadOutlined />} aria-label="刷新" onClick={refresh} loading={loading}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} aria-label="添加厂商" disabled={!canEdit} onClick={() => setCreateOpen(true)}>
            添加厂商
          </Button>
        </span>
      }
    >
      {providers.length === 0 && !loading ? (
        <Empty
          image={<ApiOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description="还没有厂商，点击右上角添加"
          style={{ padding: '60px 0' }}
        >
          <Button
            type="primary"
            icon={<PlusOutlined />}
            disabled={!canEdit}
            onClick={() => setCreateOpen(true)}
          >
            添加第一个厂商
          </Button>
        </Empty>
      ) : (
        <Table
          dataSource={providers}
          columns={columns}
          rowKey="id"
          loading={loading}
          pagination={false}
        />
      )}

      <ProviderForm
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onSubmit={handleCreate}
        loading={createLoading}
      />

      <ProviderForm
        open={editOpen}
        onCancel={() => {
          setEditOpen(false);
          setEditingProvider(null);
        }}
        onSubmit={handleEditSubmit}
        loading={updateLoading}
        provider={editingProvider}
      />

      <DiscoverResultModal
        open={discoverOpen}
        onClose={() => setDiscoverOpen(false)}
        results={discoverResults}
        providerName={discoverProviderName}
      />

      <AddModelModal
        open={addModelOpen}
        provider={addModelProvider}
        loading={addModelLoading}
        onCancel={() => {
          setAddModelOpen(false);
          setAddModelProvider(null);
        }}
        onSubmit={handleAddModel}
      />
    </Card>
  );
}
