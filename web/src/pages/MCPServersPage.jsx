import React, { useState, useEffect } from 'react';
import {
  Table, Button, Tag, Badge, Popconfirm, Drawer,
  Space, Descriptions, Tabs, Alert, message, Typography, Card,
} from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { getMCPServerTools, getMCPServerResources } from '../services/mcp';
import { COMPACT_PAGE_SIZE } from '../constants';
import useMCPServersPage from '../hooks/useMCPServersPage';

const { Title, Text } = Typography;
const TRANSPORT_COLORS = { stdio: 'blue', sse: 'green', http: 'cyan', 'streamable-http': 'purple' };
const STATUS_MAP = { connected: 'success', disconnected: 'default', error: 'error' };
const STATUS_LABELS = { connected: '已连接', disconnected: '未连接', error: '错误' };

function ServerDetailDrawer({ server, onClose }) {
  const [tools, setTools] = useState([]);
  const [resources, setResources] = useState([]);
  const [loadingTools, setLoadingTools] = useState(false);
  const [loadingRes, setLoadingRes] = useState(false);
  const [toolsError, setToolsError] = useState(null);
  const [resError, setResError] = useState(null);

  useEffect(() => {
    if (!server) return;
    setLoadingTools(true);
    setLoadingRes(true);
    setToolsError(null);
    setResError(null);

    getMCPServerTools(server.id)
      .then((r) => setTools(r.data?.tools || []))
      .catch((e) => setToolsError(e.response?.data?.error || '加载工具失败'))
      .finally(() => setLoadingTools(false));

    getMCPServerResources(server.id)
      .then((r) => setResources(r.data?.resources || []))
      .catch((e) => setResError(e.response?.data?.error || '加载资源失败'))
      .finally(() => setLoadingRes(false));
  }, [server]);

  const toolCols = [
    { title: '名称', dataIndex: 'name', width: 200, render: (v) => <Text strong>{v}</Text> },
    { title: '描述', dataIndex: 'description', ellipsis: true },
  ];
  const resCols = [
    { title: 'URI', dataIndex: 'uri', width: 200, ellipsis: true },
    { title: '名称', dataIndex: 'name' },
    { title: 'MIME', dataIndex: 'mimeType', width: 120 },
  ];

  const tabItems = [
    {
      key: 'tools',
      label: `工具（${tools.length}）`,
      children: toolsError
        ? <Alert type="error" message={toolsError} />
        : <Table size="small" dataSource={tools} columns={toolCols} rowKey="name"
            loading={loadingTools} locale={{ emptyText: '此服务器未暴露任何工具' }}
            pagination={false} />,
    },
    {
      key: 'resources',
      label: `资源（${resources.length}）`,
      children: resError
        ? <Alert type="error" message={resError} />
        : <Table size="small" dataSource={resources} columns={resCols} rowKey="uri"
            loading={loadingRes} locale={{ emptyText: '此服务器未暴露任何资源' }}
            pagination={false} />,
    },
  ];

  return (
    <Drawer
      title={server?.name || '服务器详情'}
      width={640}
      open={!!server}
      onClose={onClose}
      destroyOnClose
    >
      {server && (
        <>
          <Descriptions size="small" column={2} bordered style={{ marginBottom: 20 }}>
            <Descriptions.Item label="ID" span={2}><Text code>{server.id}</Text></Descriptions.Item>
            <Descriptions.Item label="Transport">
              <Tag color={TRANSPORT_COLORS[server.transport]}>{server.transport}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="状态">
              <Badge status={STATUS_MAP[server.status] || 'default'} text={STATUS_LABELS[server.status] || server.status} />
            </Descriptions.Item>
            <Descriptions.Item label="版本">{server.version || '-'}</Descriptions.Item>
            <Descriptions.Item label="工具数">{tools.length}</Descriptions.Item>
          </Descriptions>
          <Tabs defaultActiveKey="tools" items={tabItems} />
        </>
      )}
    </Drawer>
  );
}

export default function MCPServersPage() {
  const navigate = useNavigate();
  const {
    servers, loading,
    detailServer, setDetailServer, fetchServers, handleDisconnect,
  } = useMCPServersPage();

  const columns = [
    {
      title: '名称', dataIndex: 'name', width: 200,
      render: (v) => <Text strong>{v}</Text>,
    },
    {
      title: 'Transport', dataIndex: 'transport', width: 110,
      render: (v) => <Tag color={TRANSPORT_COLORS[v]}>{v}</Tag>,
    },
    {
      title: '状态', dataIndex: 'status', width: 110,
      render: (v) => <Badge status={STATUS_MAP[v] || 'default'} text={STATUS_LABELS[v] || v} />,
    },
    {
      title: '工具', width: 80, align: 'right',
      render: (_, r) => <Text type="secondary">{r.tools?.length ?? '-'}</Text>,
    },
    {
      title: '操作', width: 160,
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" type="link" onClick={() => setDetailServer(r)} style={{ padding: '0 4px' }}>详情</Button>
          <Popconfirm
            title="确认断开此服务器连接？"
            onConfirm={() => handleDisconnect(r.id)}
            disabled={r.status !== 'connected'}
            okText="断开" cancelText="取消"
          >
            <Button size="small" type="link" danger disabled={r.status !== 'connected'} style={{ padding: '0 4px' }}>断开</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>MCP 服务器</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>管理外部工具服务器连接</Text>
        </div>
        <Space size={8}>
          <Button icon={<ReloadOutlined />} onClick={fetchServers} loading={loading}>刷新</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/mcp/create')}>添加服务器</Button>
        </Space>
      </div>

      <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 0 } }}>
        <Table
          dataSource={servers}
          columns={columns}
          rowKey="id"
          loading={loading}
          locale={{ emptyText: '暂无已连接的 MCP 服务器' }}
          pagination={{ pageSize: COMPACT_PAGE_SIZE, showTotal: (t) => `共 ${t} 台`, style: { padding: '12px 16px' } }}
          style={{ borderRadius: 12, overflow: 'hidden' }}
        />
      </Card>

      <ServerDetailDrawer
        server={detailServer}
        onClose={() => setDetailServer(null)}
      />
    </div>
  );
}
