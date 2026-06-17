import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Badge, Button, Card, Space, Table, Tag, Typography } from 'antd';
import { useNavigate } from 'react-router-dom';

import { ServerDetailDrawer } from '../components/ServerDetailDrawer';
import { useMCPServersPage } from '../hooks/useMCPServersPage';
import type { MCPServer } from '../model/mcp';

import { COMPACT_PAGE_SIZE } from '@/constants';
import { DangerPopconfirm } from '@/shared/ui';

const { Title, Text } = Typography;

const TRANSPORT_COLORS: Record<string, string> = {
  stdio: 'blue',
  sse: 'green',
  http: 'cyan',
  'streamable-http': 'purple',
};
const STATUS_MAP: Record<string, 'success' | 'default' | 'error'> = {
  connected: 'success',
  disconnected: 'default',
  error: 'error',
};
const STATUS_LABELS: Record<string, string> = {
  connected: '已连接',
  disconnected: '未连接',
  error: '错误',
};

export const MCPServersPage = () => {
  const navigate = useNavigate();
  const { servers, loading, detailServer, setDetailServer, fetchServers, handleDisconnect } =
    useMCPServersPage();

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 200,
      render: (v: string) => <Text strong>{v}</Text>,
    },
    {
      title: 'Transport',
      dataIndex: 'transport',
      width: 110,
      render: (v: string) => <Tag color={TRANSPORT_COLORS[v]}>{v}</Tag>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 110,
      render: (v: string) => (
        <Badge status={STATUS_MAP[v] || 'default'} text={STATUS_LABELS[v] || v} />
      ),
    },
    {
      title: '工具',
      width: 80,
      align: 'right' as const,
      render: (_: unknown, r: MCPServer) => (
        <Text type="secondary">{r.tools?.length ?? '-'}</Text>
      ),
    },
    {
      title: '操作',
      width: 160,
      render: (_: unknown, r: MCPServer) => (
        <Space size={4}>
          <Button
            size="small"
            type="link"
            onClick={() => setDetailServer(r)}
            style={{ padding: '0 4px' }}
          >
            详情
          </Button>
          <DangerPopconfirm
            title="确认断开此服务器连接？"
            okText="断开"
            disabled={r.status !== 'connected'}
            onConfirm={() => handleDisconnect(r.id)}
          >
            <Button
              size="small"
              type="link"
              danger
              disabled={r.status !== 'connected'}
              style={{ padding: '0 4px' }}
            >
              断开
            </Button>
          </DangerPopconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: 20,
        }}
      >
        <div>
          <Title level={4} style={{ margin: 0 }}>
            MCP 服务器
          </Title>
          <Text type="secondary" style={{ fontSize: 13 }}>
            管理外部工具服务器连接
          </Text>
        </div>
        <Space size={8}>
          <Button icon={<ReloadOutlined />} onClick={fetchServers} loading={loading}>
            刷新
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/mcp/create')}>
            添加服务器
          </Button>
        </Space>
      </div>

      <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
        <Table
          dataSource={servers}
          columns={columns}
          rowKey="id"
          loading={loading}
          locale={{ emptyText: '暂无已连接的 MCP 服务器' }}
          pagination={{
            pageSize: COMPACT_PAGE_SIZE,
            showTotal: (t) => `共 ${t} 台`,
            style: { padding: '12px 16px' },
          }}
          style={{ borderRadius: 12, overflow: 'hidden' }}
        />
      </Card>

      <ServerDetailDrawer server={detailServer} onClose={() => setDetailServer(null)} />
    </div>
  );
};

export default MCPServersPage;
