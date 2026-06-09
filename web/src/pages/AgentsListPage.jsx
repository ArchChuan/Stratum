import React, { useState, useEffect } from 'react';
import {
  Table,
  Button,
  Space,
  Tag,
  Modal,
  Card,
  Typography,
  Input,
  notification,
  message
} from 'antd';
import { PlusOutlined, PlayCircleOutlined, DeleteOutlined, RobotOutlined, EditOutlined } from '@ant-design/icons';
import { getAllAgents, executeAgent, deleteAgent } from '../services/api';
import { useNavigate } from 'react-router-dom';

const { Search } = Input;
const { Title } = Typography;

const AgentsListPage = () => {
  const navigate = useNavigate();
  const [agents, setAgents] = useState([]);
  const [loading, setLoading] = useState(true);
  const [searchText, setSearchText] = useState('');
  const [executingAgent, setExecutingAgent] = useState(null);
  const [executionResult, setExecutionResult] = useState(null);
  const [showResultModal, setShowResultModal] = useState(false);
  const [taskModalVisible, setTaskModalVisible] = useState(false);
  const [currentTask, setCurrentTask] = useState({ query: '', context: {}, variables: {} });

  useEffect(() => {
    fetchAgents();
  }, []);

  const fetchAgents = async () => {
    try {
      setLoading(true);
      const response = await getAllAgents();
      setAgents(response.data.agents || []);
    } catch (error) {
      console.error('Error fetching agents:', error);
      notification.error({
        message: '获取代理列表失败',
        description: error.message,
      });
    } finally {
      setLoading(false);
    }
  };

  const handleExecuteAgent = (agent) => {
    setExecutingAgent(agent);
    setTaskModalVisible(true);
  };

  const handleTaskSubmit = async () => {
    if (!executingAgent) return;
    
    try {
      const response = await executeAgent(executingAgent.id, currentTask);
      setExecutionResult(response.data);
      setTaskModalVisible(false);
      setShowResultModal(true);
    } catch (error) {
      console.error('Error executing agent:', error);
      setExecutionResult({ error: error.message });
      setTaskModalVisible(false);
      setShowResultModal(true);
    }
  };

  const handleDeleteAgent = (agentId, agentName) => {
    Modal.confirm({
      title: '确认删除',
      content: `确定要删除代理 "${agentName}" 吗？此操作不可恢复。`,
      okText: '删除',
      okType: 'danger',
      cancelText: '取消',
      onOk: async () => {
        try {
          await deleteAgent(agentId);
          message.success('代理删除成功');
          fetchAgents();
        } catch (error) {
          if (error.response?.status !== 403) {
            message.error(error.response?.data?.error || '删除失败');
          }
        }
      },
    });
  };

  // 过滤代理列表
  const filteredAgents = agents.filter(agent =>
    agent.name.toLowerCase().includes(searchText.toLowerCase()) ||
    agent.description.toLowerCase().includes(searchText.toLowerCase())
  );

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      key: 'id',
      width: 200,
      ellipsis: true,
    },
    {
      title: '代理名称',
      dataIndex: 'name',
      key: 'name',
      sorter: (a, b) => a.name.localeCompare(b.name),
    },
    {
      title: '模型',
      dataIndex: 'llmModel',
      key: 'llmModel',
    },
    {
      title: '描述',
      dataIndex: 'description',
      key: 'description',
      ellipsis: true,
    },
    {
      title: '允许技能数',
      dataIndex: 'allowedSkills',
      key: 'allowedSkillsCount',
      render: (text, record) => record.allowedSkills?.length || 0,
    },
    {
      title: '创建时间',
      dataIndex: 'createdAt',
      key: 'createdAt',
      render: (date) => date ? new Date(date).toLocaleString() : '-',
    },
    {
      title: '操作',
      key: 'actions',
      render: (_, record) => (
        <Space size="middle">
          <Button
            type="link"
            icon={<EditOutlined />}
            onClick={() => navigate(`/agents/${record.id}/edit`)}
          >
            编辑
          </Button>
          <Button
            type="link"
            icon={<PlayCircleOutlined />}
            onClick={() => handleExecuteAgent(record)}
          >
            执行
          </Button>
          <Button 
            type="link" 
            danger 
            icon={<DeleteOutlined />}
            onClick={() => handleDeleteAgent(record.id, record.name)}
          >
            删除
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div style={{ display: 'flex', alignItems: 'center' }}>
          <RobotOutlined style={{ fontSize: '24px', marginRight: '10px' }} />
          <Title level={2} style={{ margin: 0 }}>智能代理管理</Title>
        </div>
        <Space>
          <Search
            placeholder="搜索代理名称或描述"
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            style={{ width: 300 }}
          />
          <Button type="primary" icon={<PlusOutlined />} href="/agents/create">
            创建代理
          </Button>
        </Space>
      </div>

      <Card>
        <Table 
          dataSource={filteredAgents} 
          columns={columns} 
          rowKey="id" 
          loading={loading}
          pagination={{ 
            pageSize: 10,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total) => `总计 ${total} 条`,
          }}
        />
      </Card>

      {/* 任务输入模态框 */}
      <Modal
        title={`执行代理 - ${executingAgent?.name}`}
        open={taskModalVisible}
        onOk={handleTaskSubmit}
        onCancel={() => setTaskModalVisible(false)}
        width={800}
      >
        <div style={{ marginBottom: 16 }}>
          <label style={{ display: 'block', marginBottom: 8 }}>任务查询:</label>
          <Input.TextArea
            rows={4}
            value={currentTask.query}
            onChange={(e) => setCurrentTask({...currentTask, query: e.target.value})}
            placeholder="请输入您希望代理执行的任务..."
          />
        </div>
        <div style={{ marginBottom: 16 }}>
          <label style={{ display: 'block', marginBottom: 8 }}>上下文 (可选):</label>
          <Input.TextArea
            rows={3}
            value={JSON.stringify(currentTask.context || {}, null, 2)}
            onChange={(e) => {
              try {
                const parsed = JSON.parse(e.target.value);
                setCurrentTask({...currentTask, context: parsed});
              } catch (e) {
                // 如果解析失败，则不更新
              }
            }}
            placeholder='输入上下文信息，例如: {"previous_result": "some_value"}'
          />
        </div>
      </Modal>

      {/* 执行结果模态框 */}
      <Modal
        title={`执行结果 - ${executingAgent?.name}`}
        open={showResultModal}
        onCancel={() => {
          setShowResultModal(false);
          setExecutingAgent(null);
          setExecutionResult(null);
        }}
        footer={[
          <Button 
            key="close" 
            onClick={() => {
              setShowResultModal(false);
              setExecutingAgent(null);
              setExecutionResult(null);
            }}
          >
            关闭
          </Button>
        ]}
        width={800}
      >
        <pre style={{ 
          background: '#f5f5f5', 
          padding: '16px', 
          borderRadius: '4px',
          maxHeight: '400px',
          overflow: 'auto',
          whiteSpace: 'pre-wrap',
          wordWrap: 'break-word'
        }}>
          {JSON.stringify(executionResult, null, 2)}
        </pre>
      </Modal>
    </div>
  );
};

export default AgentsListPage;
