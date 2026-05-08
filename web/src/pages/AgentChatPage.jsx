import React, { useState, useEffect, useRef } from 'react';
import { 
  Card, 
  Typography, 
  Input, 
  Button, 
  List, 
  Select, 
  Space, 
  Alert,
  Spin,
  Tag
} from 'antd';
import { SendOutlined, LoadingOutlined, RobotOutlined, UserOutlined } from '@ant-design/icons';
import { getAllAgents, executeAgent } from '../services/api';

const { Title, Text } = Typography;
const { TextArea } = Input;
const { Option } = Select;

const AgentChatPage = () => {
  const [agents, setAgents] = useState([]);
  const [selectedAgent, setSelectedAgent] = useState(null);
  const [messages, setMessages] = useState([]);
  const [inputMessage, setInputMessage] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [agentDetails, setAgentDetails] = useState(null);
  const messagesEndRef = useRef(null);

  useEffect(() => {
    loadAgents();
  }, []);

  useEffect(() => {
    // 当选择代理时，获取代理详情
    if (selectedAgent) {
      const agentDetail = agents.find(a => a.id === selectedAgent);
      setAgentDetails(agentDetail);
    }
  }, [selectedAgent, agents]);

  const loadAgents = async () => {
    try {
      const response = await getAllAgents();
      setAgents(response.data.agents || []);
    } catch (err) {
      setError('加载智能代理失败: ' + err.message);
    }
  };

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  };

  useEffect(() => {
    scrollToBottom();
  }, [messages]);

  const handleSendMessage = async () => {
    if (!inputMessage.trim() || !selectedAgent) {
      return;
    }

    // 添加用户消息
    const userMessage = {
      id: Date.now(),
      type: 'user',
      content: inputMessage,
      timestamp: new Date().toLocaleTimeString()
    };

    setMessages(prev => [...prev, userMessage]);
    setInputMessage('');
    setLoading(true);
    setError('');

    try {
      // 执行Agent
      const task = {
        query: inputMessage,
        context: {},
        variables: {}
      };

      const response = await executeAgent(selectedAgent, task);
      
      // 添加Agent响应
      const agentMessage = {
        id: Date.now() + 1,
        type: 'agent',
        content: typeof response.data.result === 'string' 
          ? response.data.result 
          : JSON.stringify(response.data.result, null, 2),
        timestamp: new Date().toLocaleTimeString(),
        steps: response.data.steps || [],
        status: response.data.status
      };

      setMessages(prev => [...prev, agentMessage]);
    } catch (err) {
      const errorMessage = {
        id: Date.now() + 1,
        type: 'agent',
        content: `执行出错: ${err.message}`,
        timestamp: new Date().toLocaleTimeString(),
        isError: true
      };
      setMessages(prev => [...prev, errorMessage]);
    } finally {
      setLoading(false);
    }
  };

  const handleKeyPress = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSendMessage();
    }
  };

  const agentOptions = agents.map(agent => (
    <Option key={agent.id} value={agent.id}>
      {agent.name}
    </Option>
  ));

  return (
    <div style={{ maxWidth: '900px', margin: '0 auto' }}>
      <Title level={2} style={{ textAlign: 'center', marginBottom: 24 }}>
        智能代理对话
      </Title>

      {error && (
        <Alert
          message="错误"
          description={error}
          type="error"
          closable
          style={{ marginBottom: 16 }}
        />
      )}

      <Card style={{ marginBottom: 16 }}>
        <Space direction="vertical" style={{ width: '100%' }}>
          <div>
            <Text strong style={{ marginRight: 8 }}>选择智能代理:</Text>
            <Select
              style={{ width: 300 }}
              placeholder="请选择一个智能代理"
              value={selectedAgent}
              onChange={setSelectedAgent}
              loading={agents.length === 0}
            >
              {agentOptions}
            </Select>
            
            {agentDetails && (
              <div style={{ marginTop: 10, padding: '10px', backgroundColor: '#f9f9f9', borderRadius: '4px' }}>
                <Text strong>{agentDetails.name}</Text>
                <div style={{ marginTop: 5 }}>
                  <Tag color="blue">模型: {agentDetails.llmModel}</Tag>
                  <Tag color="green">最大迭代: {agentDetails.maxIterations}</Tag>
                  <Tag color="orange">技能数: {agentDetails.allowedSkills.length}</Tag>
                </div>
                <div style={{ marginTop: 5, fontSize: '13px', color: '#666' }}>
                  {agentDetails.description}
                </div>
              </div>
            )}
          </div>

          {!selectedAgent && agents.length > 0 && (
            <Alert
              message="提示"
              description="请先从下拉菜单中选择一个智能代理"
              type="info"
              showIcon
            />
          )}
        </Space>
      </Card>

      <Card 
        style={{ 
          height: 500, 
          overflowY: 'auto', 
          marginBottom: 16,
          display: 'flex',
          flexDirection: 'column'
        }}
      >
        {messages.length === 0 ? (
          <div style={{ 
            flex: 1, 
            display: 'flex', 
            justifyContent: 'center', 
            alignItems: 'center',
            color: '#aaa'
          }}>
            <div style={{ textAlign: 'center' }}>
              <RobotOutlined style={{ fontSize: '48px', marginBottom: '16px', color: '#1890ff' }} />
              <Text>选择一个智能代理并开始对话...</Text>
              <div style={{ marginTop: '16px', fontSize: '13px', color: '#888' }}>
                您可以直接输入命令或问题，智能代理将为您处理
              </div>
            </div>
          </div>
        ) : (
          <List
            dataSource={messages}
            renderItem={item => (
              <List.Item 
                style={{ 
                  justifyContent: item.type === 'user' ? 'flex-end' : 'flex-start',
                  padding: '8px 0'
                }}
              >
                <div 
                  style={{
                    maxWidth: '80%',
                    padding: '12px 16px',
                    borderRadius: '18px',
                    backgroundColor: item.type === 'user' ? '#e3f2fd' : '#f5f5f5',
                    alignSelf: 'flex-start',
                    boxShadow: '0 1px 2px rgba(0,0,0,0.1)'
                  }}
                >
                  <div style={{ marginBottom: 4 }}>
                    <Space size="small">
                      {item.type === 'user' ? <UserOutlined /> : <RobotOutlined />}
                      <Text strong>
                        {item.type === 'user' ? '您' : agents.find(a => a.id === selectedAgent)?.name || '智能代理'}
                      </Text>
                    </Space>
                    <Text type="secondary" style={{ float: 'right', fontSize: '12px' }}>
                      {item.timestamp}
                    </Text>
                  </div>
                  
                  {item.isError ? (
                    <Text type="danger">{item.content}</Text>
                  ) : (
                    <Text code={item.type === 'agent'} style={{ wordBreak: 'break-word' }}>
                      {item.content}
                    </Text>
                  )}
                  
                  {item.steps && item.steps.length > 0 && (
                    <div style={{ marginTop: 8, paddingTop: 8, borderTop: '1px solid #eee' }}>
                      <Text type="secondary" strong>执行步骤:</Text>
                      <ul style={{ margin: 0, paddingInlineStart: 20, fontSize: '12px' }}>
                        {item.steps.map((step, idx) => (
                          <li key={idx}>
                            <strong>步骤 {step.iteration}</strong>: {step.action}
                            {step.tool && <span> (工具: {step.tool})</span>}
                            {step.action === 'final_response' && step.output && (
                              <div style={{ marginTop: 4, paddingLeft: 10 }}>
                                最终回应: {typeof step.output === 'string' ? step.output : JSON.stringify(step.output)}
                              </div>
                            )}
                          </li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>
              </List.Item>
            )}
          />
        )}
        <div ref={messagesEndRef} />
      </Card>

      <Card>
        <Space direction="vertical" style={{ width: '100%' }}>
          <TextArea
            placeholder="输入您的问题或命令..."
            value={inputMessage}
            onChange={(e) => setInputMessage(e.target.value)}
            onPressEnter={handleKeyPress}
            disabled={!selectedAgent || loading}
            rows={4}
          />
          <div style={{ textAlign: 'right' }}>
            <Text type="secondary" style={{ fontSize: '12px', marginRight: 10 }}>
              {selectedAgent ? `正在与 ${agents.find(a => a.id === selectedAgent)?.name} 对话` : '请选择一个智能代理'}
            </Text>
            <Button
              type="primary"
              icon={loading ? <LoadingOutlined /> : <SendOutlined />}
              onClick={handleSendMessage}
              disabled={!selectedAgent || !inputMessage.trim() || loading}
            >
              发送
            </Button>
          </div>
        </Space>
      </Card>
    </div>
  );
};

export default AgentChatPage;