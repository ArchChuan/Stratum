import React, { useState, useEffect } from 'react';
import {
  Table, Button, Tag, Badge, Popconfirm, Drawer, Form, Input,
  Select, InputNumber, Space, Descriptions, Tabs, Alert, message,
} from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import {
  getMCPServers, connectMCPServer, disconnectMCPServer,
  getMCPServerTools, getMCPServerResources,
} from '../services/api';

const TRANSPORT_COLORS = { stdio: 'blue', sse: 'green', http: 'cyan' };
const STATUS_MAP = { connected: 'success', disconnected: 'default', error: 'error' };

function parseArgs(str) {
  return (str || '').split(/\s+/).filter(Boolean);
}

function parseEnv(str) {
  const result = {};
  (str || '').split('\n').forEach((line) => {
    const idx = line.indexOf('=');
    if (idx > 0) result[line.slice(0, idx).trim()] = line.slice(idx + 1);
  });
  return result;
}

function ConnectDrawer({ open, onClose, onSuccess }) {
  const [form] = Form.useForm();
  const [submitting, setSubmitting] = useState(false);
  const transport = Form.useWatch('transport', form);

  const handleFinish = async (values) => {
    setSubmitting(true);
    try {
      const cfg = {
        id: values.id || crypto.randomUUID(),
        name: values.name,
        transport: values.transport,
        command: values.command || '',
        args: parseArgs(values.args),
        env: parseEnv(values.env),
        url: values.url || '',
        timeout: (values.timeout_sec || 30) * 1e9,
      };
      await connectMCPServer(cfg);
      message.success('服务器连接成功');
      form.resetFields();
      onSuccess();
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '连接失败');
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Drawer
      title="连接 MCP 服务器"
      width={480}
      open={open}
      onClose={() => { form.resetFields(); onClose(); }}
      destroyOnClose
    >
      <Form form={form} layout="vertical" onFinish={handleFinish}>
        <Form.Item label="服务器 ID（留空自动生成）" name="id">
          <Input placeholder="my-server-id" />
        </Form.Item>
        <Form.Item label="服务器名称" name="name" rules={[{ required: true, message: '请输入名称' }]}>
          <Input maxLength={64} />
        </Form.Item>
        <Form.Item label="Transport" name="transport" rules={[{ required: true, message: '请选择 Transport' }]}>
          <Select options={[
            { value: 'stdio', label: 'stdio（子进程）' },
            { value: 'sse',   label: 'sse（SSE 长连接）' },
            { value: 'http',  label: 'http（HTTP 轮询）' },
          ]} />
        </Form.Item>
        {transport === 'stdio' && (
          <>
            <Form.Item label="命令（command）" name="command" rules={[{ required: true, message: '请输入命令' }]}>
              <Input placeholder="node" />
            </Form.Item>
            <Form.Item label="参数（args，空格分隔）" name="args">
              <Input placeholder="server.js --port 3000" />
            </Form.Item>
            <Form.Item label="环境变量（每行 KEY=VALUE）" name="env">
              <Input.TextArea rows={4} placeholder="API_KEY=xxx&#10;DEBUG=true" />
            </Form.Item>
          </>
        )}
        {(transport === 'sse' || transport === 'http') && (
          <Form.Item label="URL" name="url" rules={[{ required: true, message: '请输入 URL' }]}>
            <Input placeholder="http://localhost:3000/mcp" />
          </Form.Item>
        )}
        <Form.Item label="超时（秒）" name="timeout_sec" initialValue={30}>
          <InputNumber min={1} max={300} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" block loading={submitting}>连接</Button>
        </Form.Item>
      </Form>
    </Drawer>
  );
}

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
    { title: '名称', dataIndex: 'name', width: 200 },
    { title: '描述', dataIndex: 'description', ellipsis: true },
  ];
  const resCols = [
    { title: 'URI', dataIndex: 'uri', width: 200, ellipsis: true },
    { title: '名称', dataIndex: 'name' },
    { title: 'MIME', dataIndex: 'mimeType' },
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
          <Descriptions size="small" column={4} style={{ marginBottom: 16 }}>
            <Descriptions.Item label="ID">{server.id}</Descriptions.Item>
            <Descriptions.Item label="Transport">
              <Tag color={TRANSPORT_COLORS[server.transport]}>{server.transport}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="状态">
              <Badge status={STATUS_MAP[server.status] || 'default'} text={server.status} />
            </Descriptions.Item>
            <Descriptions.Item label="版本">{server.version || '-'}</Descriptions.Item>
          </Descriptions>
          <Tabs defaultActiveKey="tools" items={tabItems} />
        </>
      )}
    </Drawer>
  );
}

export default function MCPServersPage() {
  const [servers, setServers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [connectOpen, setConnectOpen] = useState(false);
  const [detailServer, setDetailServer] = useState(null);

  const fetchServers = async () => {
    setLoading(true);
    try {
      const res = await getMCPServers();
      setServers(res.data?.servers || []);
    } catch {
      message.error('获取 MCP 服务器列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchServers(); }, []);

  const handleDisconnect = async (id) => {
    try {
      await disconnectMCPServer(id);
      message.success('已断开连接');
      fetchServers();
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '断开失败');
      }
    }
  };

  const columns = [
    { title: '名称', dataIndex: 'name', width: 200, ellipsis: true },
    { title: 'ID', dataIndex: 'id', ellipsis: true, width: 180 },
    {
      title: 'Transport', dataIndex: 'transport', width: 100,
      render: (v) => <Tag color={TRANSPORT_COLORS[v]}>{v}</Tag>,
    },
    {
      title: '状态', dataIndex: 'status', width: 110,
      render: (v) => <Badge status={STATUS_MAP[v] || 'default'} text={v} />,
    },
    {
      title: 'Tools', width: 80,
      render: (_, r) => r.tools?.length ?? '-',
    },
    {
      title: '操作', width: 160,
      render: (_, r) => (
        <Space>
          <Button size="small" onClick={() => setDetailServer(r)}>查看</Button>
          <Popconfirm
            title="确认断开此服务器连接？"
            onConfirm={() => handleDisconnect(r.id)}
            disabled={r.status !== 'connected'}
          >
            <Button size="small" danger disabled={r.status !== 'connected'}>断开</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <span style={{ fontSize: 16, fontWeight: 500 }}>MCP 服务器</span>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setConnectOpen(true)}>
          连接服务器
        </Button>
      </div>
      <Table
        dataSource={servers}
        columns={columns}
        rowKey="id"
        loading={loading}
        locale={{ emptyText: '暂无已连接的 MCP 服务器' }}
      />
      <ConnectDrawer
        open={connectOpen}
        onClose={() => setConnectOpen(false)}
        onSuccess={() => { setConnectOpen(false); fetchServers(); }}
      />
      <ServerDetailDrawer
        server={detailServer}
        onClose={() => setDetailServer(null)}
      />
    </>
  );
}
