import {
  ApiOutlined,
  DeleteOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import {
  Button,
  Card,
  Empty,
  Modal,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { useCallback, useState } from 'react';

import { llmApi } from '../api/llm.api';
import { DiscoverResultModal } from '../components/DiscoverResultModal';
import { ProviderForm } from '../components/ProviderForm';
import { useProviders } from '../hooks/useProviders';
import type { CreateProviderInput, Model, Provider, ProviderKind } from '../model/llm';

import { useTenantRole } from '@/modules/iam';
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

export function ProviderListPage() {
  const { providers, loading, createLoading, refresh, createProvider, deleteProvider } = useProviders();
  const { isAdmin } = useTenantRole();
  const [createOpen, setCreateOpen] = useState(false);
  const [discoveringIds, setDiscoveringIds] = useState<Set<string>>(new Set());
  const [discoverResults, setDiscoverResults] = useState<Model[]>([]);
  const [discoverProviderName, setDiscoverProviderName] = useState('');
  const [discoverOpen, setDiscoverOpen] = useState(false);

  const handleCreate = useCallback(
    async (values: CreateProviderInput) => {
      await createProvider(values);
      setCreateOpen(false);
    },
    [createProvider],
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
      width: 240,
      render: (_: unknown, record: Provider) => (
        <span style={{ display: 'flex', gap: 8 }}>
          <Button
            size="small"
            onClick={() => handleDiscover(record)}
            loading={discoveringIds.has(record.id)}
          >
            发现模型
          </Button>
          <Button size="small" onClick={() => handleHealthCheck(record)}>
            健康检查
          </Button>
          {isAdmin && (
            <Tooltip title="删除">
              <Button
                size="small"
                danger
                icon={<DeleteOutlined />}
                onClick={() => handleDelete(record)}
              />
            </Tooltip>
          )}
        </span>
      ),
    },
  ];

  return (
    <Card
      title="厂商管理"
      extra={
        <span style={{ display: 'flex', gap: 8 }}>
          <Button icon={<ReloadOutlined />} onClick={refresh} loading={loading}>
            刷新
          </Button>
          {isAdmin && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              添加厂商
            </Button>
          )}
        </span>
      }
    >
      {providers.length === 0 && !loading ? (
        <Empty
          image={<ApiOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description={isAdmin ? '还没有厂商，点击右上角添加' : '还没有厂商'}
          style={{ padding: '60px 0' }}
        >
          {isAdmin && (
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setCreateOpen(true)}
            >
              添加第一个厂商
            </Button>
          )}
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

      <DiscoverResultModal
        open={discoverOpen}
        onClose={() => setDiscoverOpen(false)}
        results={discoverResults}
        providerName={discoverProviderName}
      />
    </Card>
  );
}
