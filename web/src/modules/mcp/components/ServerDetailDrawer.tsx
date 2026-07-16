import { Alert, Badge, Drawer, Descriptions, Flex, Select, Tabs, Tag, Typography, message } from 'antd';
import { useEffect, useState } from 'react';

import { mcpApi } from '../api/mcp.api';
import type { MCPResource, MCPServer, MCPTool, MCPToolPolicy, MCPToolRiskLevel } from '../model/mcp';

import { extractErrorMessage } from '@/shared/lib';
import { ResponsiveDataView } from '@/shared/ui';

const { Text } = Typography;

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

interface Props {
  server: MCPServer | null;
	onClose: () => void;
	isAdmin?: boolean;
}

export const ServerDetailDrawer = ({ server, onClose, isAdmin = false }: Props) => {
  const [tools, setTools] = useState<MCPTool[]>([]);
  const [resources, setResources] = useState<MCPResource[]>([]);
  const [loadingTools, setLoadingTools] = useState(false);
  const [loadingRes, setLoadingRes] = useState(false);
  const [toolsError, setToolsError] = useState<string | null>(null);
	const [resError, setResError] = useState<string | null>(null);
	const [policies, setPolicies] = useState<MCPToolPolicy[]>([]);

  useEffect(() => {
    if (!server) return;
    setLoadingTools(true);
    setLoadingRes(true);
    setToolsError(null);
    setResError(null);

	mcpApi
      .tools(server.id)
      .then(setTools)
      .catch((e) => setToolsError(extractErrorMessage(e) || '加载工具失败'))
      .finally(() => setLoadingTools(false));

    mcpApi
      .resources(server.id)
      .then(setResources)
      .catch((e) => setResError(extractErrorMessage(e) || '加载资源失败'))
		.finally(() => setLoadingRes(false));
	mcpApi.toolPolicies().then(setPolicies).catch(() => setPolicies([]));
	}, [server]);

	const riskFor = (toolName: string): MCPToolRiskLevel => policies.find((p) => p.serverId === server?.id && p.toolName === toolName)?.riskLevel || 'unclassified';
	const changeRisk = async (toolName: string, riskLevel: MCPToolRiskLevel) => {
		if (!server) return;
		try { await mcpApi.setToolPolicy(server.id, toolName, riskLevel); setPolicies((rows) => [...rows.filter((p) => p.serverId !== server.id || p.toolName !== toolName), { serverId: server.id, toolName, riskLevel }]); message.success('工具风险策略已更新'); }
		catch (err) { message.error(extractErrorMessage(err) || '更新风险策略失败'); }
	};

  const toolCols = [
    { title: '名称', dataIndex: 'name', width: 200, render: (v: string) => <Text strong>{v}</Text> },
		{ title: '描述', dataIndex: 'description', ellipsis: true },
		{ title: '风险等级', dataIndex: 'name', width: 180, render: (name: string) => <Select aria-label={`${name} 风险等级`} disabled={!isAdmin} value={riskFor(name)} style={{ width: '100%' }} onChange={(value) => changeRisk(name, value)} options={RISK_OPTIONS} /> },
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
      children: toolsError ? (
        <Alert type="error" message={toolsError} />
      ) : (
        <ResponsiveDataView
          rows={tools}
          size="small"
          columns={toolCols}
          rowKey="name"
          loading={loadingTools}
          emptyText="此服务器未暴露任何工具"
          pagination={false}
          renderMobileItem={(tool) => (
            <div style={{ padding: '10px 0', borderBottom: '1px solid #f0f0f0' }}>
              <Text strong>{tool.name}</Text>
              <Text type="secondary" style={{ display: 'block', marginTop: 4 }}>
				{tool.description || '暂无描述'}
			</Text>
			<Select aria-label={`${tool.name} 风险等级`} disabled={!isAdmin} value={riskFor(tool.name)} style={{ width: '100%', marginTop: 8 }} onChange={(value) => changeRisk(tool.name, value)} options={RISK_OPTIONS} />
            </div>
          )}
        />
      ),
    },
    {
      key: 'resources',
      label: `资源（${resources.length}）`,
      children: resError ? (
        <Alert type="error" message={resError} />
      ) : (
        <ResponsiveDataView
          rows={resources}
          size="small"
          columns={resCols}
          rowKey="uri"
          loading={loadingRes}
          emptyText="此服务器未暴露任何资源"
          pagination={false}
          renderMobileItem={(resource) => (
            <div style={{ padding: '10px 0', borderBottom: '1px solid #f0f0f0' }}>
              <Flex justify="space-between" align="center" gap={8}>
                <Text strong ellipsis>{resource.name || resource.uri}</Text>
                <Tag>{resource.mimeType || '未知类型'}</Tag>
              </Flex>
              <Text type="secondary" ellipsis style={{ display: 'block', marginTop: 4 }}>
                {resource.uri}
              </Text>
            </div>
          )}
        />
      ),
    },
  ];

  return (
    <Drawer
      title={server?.name || '服务器详情'}
      width={640}
      open={!!server}
      onClose={onClose}
      destroyOnHidden
    >
      {server && (
        <>
          <Descriptions size="small" column={2} bordered style={{ marginBottom: 20 }}>
            <Descriptions.Item label="ID" span={2}>
              <Text code>{server.id}</Text>
            </Descriptions.Item>
            <Descriptions.Item label="Transport">
              <Tag color={TRANSPORT_COLORS[server.transport]}>{server.transport}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="状态">
              <Badge
                status={STATUS_MAP[server.status] || 'default'}
                text={STATUS_LABELS[server.status] || server.status}
              />
            </Descriptions.Item>
            <Descriptions.Item label="版本">{server.version || '-'}</Descriptions.Item>
            <Descriptions.Item label="工具数">{tools.length}</Descriptions.Item>
          </Descriptions>
          <Tabs defaultActiveKey="tools" items={tabItems} />
        </>
      )}
    </Drawer>
  );
};

const RISK_OPTIONS: Array<{ value: MCPToolRiskLevel; label: string }> = [
	{ value: 'unclassified', label: '未分类（需审批）' },
	{ value: 'read', label: '只读' },
	{ value: 'write_reversible', label: '可逆写入' },
	{ value: 'destructive', label: '破坏性（需审批）' },
];
