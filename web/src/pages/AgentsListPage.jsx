import React from 'react';
import {
  Button, Space, Tag, Input, Modal, Typography, Card,
  Row, Col, Tooltip, Popconfirm, Badge, Empty, Skeleton,
} from 'antd';
import {
  PlusOutlined, PlayCircleOutlined, DeleteOutlined, EditOutlined,
  RobotOutlined, SearchOutlined, ThunderboltOutlined,
} from '@ant-design/icons';
import useAgentsListPage from '../hooks/useAgentsListPage';

const { Title, Text, Paragraph } = Typography;
const { TextArea } = Input;

const MODEL_COLORS = { 'gpt-4': '#1677ff', 'gpt-3.5-turbo': '#52c41a', 'glm-4': '#722ed1', 'glm-4-flash': '#13c2c2', 'qwen-plus': '#fa8c16', 'qwen-turbo': '#fa541c' };
const modelColor = (m) => MODEL_COLORS[m] || '#8c8c8c';

const AgentCard = ({ agent, onExecute, onDelete, onEdit }) => (
  <Card
    style={{ borderRadius: 12, border: '1px solid #f0f0f0', height: '100%', display: 'flex', flexDirection: 'column' }}
    styles={{ body: { padding: 20, flex: 1, display: 'flex', flexDirection: 'column' } }}
    hoverable
  >
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 12 }}>
      <div style={{
        width: 40, height: 40, borderRadius: 10,
        background: 'linear-gradient(135deg, #e6f4ff 0%, #f9f0ff 100%)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        flexShrink: 0,
      }}>
        <RobotOutlined style={{ fontSize: 18, color: '#1677ff' }} />
      </div>
      <Tag
        style={{
          border: 'none', borderRadius: 6, fontSize: 11,
          background: `${modelColor(agent.llmModel)}18`,
          color: modelColor(agent.llmModel),
          fontWeight: 500,
        }}
      >
        {agent.llmModel}
      </Tag>
    </div>

    <Text strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>{agent.name}</Text>
    <Paragraph
      type="secondary"
      ellipsis={{ rows: 2 }}
      style={{ fontSize: 13, marginBottom: 12, flex: 1, marginTop: 0 }}
    >
      {agent.description || '暂无描述'}
    </Paragraph>

    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingTop: 12, borderTop: '1px solid #f5f5f5' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
        <ThunderboltOutlined style={{ color: '#8c8c8c', fontSize: 12 }} />
        <Text type="secondary" style={{ fontSize: 12 }}>
          {agent.allowedSkills?.length || 0} 技能
        </Text>
      </div>
      <Space size={4}>
        <Tooltip title="执行">
          <Button
            type="text" size="small" icon={<PlayCircleOutlined />}
            onClick={() => onExecute(agent)}
            style={{ color: '#1677ff' }}
          />
        </Tooltip>
        <Tooltip title="编辑">
          <Button type="text" size="small" icon={<EditOutlined />} onClick={() => onEdit(agent)} />
        </Tooltip>
        <Tooltip title="删除">
          <Popconfirm
            title={`确定删除 "${agent.name}" 吗？`}
            onConfirm={() => onDelete(agent.id, agent.name)}
            okText="删除" okType="danger" cancelText="取消"
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Tooltip>
      </Space>
    </div>
  </Card>
);

const AgentsListPage = () => {
  const {
    agents: filteredAgents, loading, searchText, setSearchText,
    executingAgent, setExecutingAgent,
    executionResult, setExecutionResult,
    showResultModal, setShowResultModal,
    taskModalVisible, setTaskModalVisible,
    taskQuery, setTaskQuery,
    taskContext, setTaskContext,
    executing, navigate,
    handleTaskSubmit, handleDeleteAgent,
  } = useAgentsListPage();

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>Agent 列表</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>管理所有智能 Agent</Text>
        </div>
        <Space size={8}>
          <Input
            placeholder="搜索 Agent..."
            prefix={<SearchOutlined style={{ color: '#bfbfbf' }} />}
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            allowClear
            style={{ width: 220 }}
          />
          <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/agents/create')}>
            创建 Agent
          </Button>
        </Space>
      </div>

      {loading ? (
        <Row gutter={[16, 16]}>
          {[1, 2, 3, 4, 5, 6].map(i => (
            <Col xs={24} sm={12} lg={8} xl={6} key={i}>
              <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 20 } }}>
                <Skeleton active avatar paragraph={{ rows: 2 }} />
              </Card>
            </Col>
          ))}
        </Row>
      ) : filteredAgents.length === 0 ? (
        <Empty
          image={<RobotOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description={searchText ? '没有找到匹配的 Agent' : '还没有 Agent，点击右上角创建'}
          style={{ padding: '60px 0' }}
        >
          {!searchText && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/agents/create')}>
              创建第一个 Agent
            </Button>
          )}
        </Empty>
      ) : (
        <Row gutter={[16, 16]}>
          {filteredAgents.map(agent => (
            <Col xs={24} sm={12} lg={8} xl={6} key={agent.id}>
              <AgentCard
                agent={agent}
                onExecute={(a) => { setExecutingAgent(a); setTaskQuery(''); setTaskContext('{}'); setTaskModalVisible(true); }}
                onDelete={handleDeleteAgent}
                onEdit={(a) => navigate(`/agents/${a.id}/edit`)}
              />
            </Col>
          ))}
        </Row>
      )}

      <Modal
        title={<Space><PlayCircleOutlined />{`执行 ${executingAgent?.name}`}</Space>}
        open={taskModalVisible}
        onOk={handleTaskSubmit}
        onCancel={() => setTaskModalVisible(false)}
        okText="执行" cancelText="取消"
        confirmLoading={executing}
        width={560}
        destroyOnClose
      >
        <Space direction="vertical" style={{ width: '100%' }} size={16}>
          <div>
            <div style={{ marginBottom: 6, fontWeight: 500 }}>任务内容 <span style={{ color: '#ff4d4f' }}>*</span></div>
            <TextArea
              rows={4}
              value={taskQuery}
              onChange={(e) => setTaskQuery(e.target.value)}
              placeholder="输入你希望 Agent 执行的任务..."
              autoFocus
            />
          </div>
          <div>
            <div style={{ marginBottom: 6, fontWeight: 500 }}>上下文 <Text type="secondary" style={{ fontWeight: 400, fontSize: 12 }}>（JSON，可选）</Text></div>
            <TextArea
              rows={3}
              value={taskContext}
              onChange={(e) => setTaskContext(e.target.value)}
              placeholder='{"key": "value"}'
              style={{ fontFamily: 'monospace', fontSize: 13 }}
            />
          </div>
        </Space>
      </Modal>

      <Modal
        title={<Space><Badge status={executionResult?.error ? 'error' : 'success'} />{`执行结果 — ${executingAgent?.name}`}</Space>}
        open={showResultModal}
        onCancel={() => { setShowResultModal(false); setExecutingAgent(null); setExecutionResult(null); }}
        footer={<Button onClick={() => { setShowResultModal(false); setExecutingAgent(null); setExecutionResult(null); }}>关闭</Button>}
        width={680}
        destroyOnClose
      >
        <pre style={{
          background: '#f5f7fa', padding: 16, borderRadius: 8,
          maxHeight: 400, overflow: 'auto',
          whiteSpace: 'pre-wrap', wordWrap: 'break-word',
          fontSize: 13, lineHeight: 1.6,
        }}>
          {JSON.stringify(executionResult, null, 2)}
        </pre>
      </Modal>
    </div>
  );
};

export default AgentsListPage;
