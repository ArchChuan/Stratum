import React, { useState, useEffect } from 'react';
import { 
  Table, 
  Card, 
  Typography, 
  Tag,
  Empty,
  Button,
  Space 
} from 'antd';
import { ReloadOutlined } from '@ant-design/icons';

const { Title } = Typography;

// 模拟执行历史数据，实际项目中应该从API获取
const mockExecutionHistory = [
  {
    id: 'exec-1',
    skillId: 'skill-1',
    skillName: 'Python Calculator',
    status: 'success',
    result: { result: 8 },
    executedAt: '2026-05-08T22:30:00Z',
    input: { a: 5, b: 3 }
  },
  {
    id: 'exec-2',
    skillId: 'skill-2',
    skillName: 'Text Summarizer',
    status: 'success',
    result: { result: 'This is a summary...' },
    executedAt: '2026-05-08T22:25:00Z',
    input: { text: 'Long text to summarize...' }
  },
  {
    id: 'exec-3',
    skillId: 'skill-3',
    skillName: 'Data Validator',
    status: 'error',
    result: { error: 'Validation failed: Invalid format' },
    executedAt: '2026-05-08T22:20:00Z',
    input: { data: 'invalid_data' }
  }
];

const ExecutionHistoryPage = () => {
  const [executions, setExecutions] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    // 在实际项目中，这里应该从API获取执行历史
    // loadExecutionHistory();
    setExecutions(mockExecutionHistory);
  }, []);

  const loadExecutionHistory = async () => {
    setLoading(true);
    try {
      // 实际API调用示例
      // const response = await getExecutionHistory();
      // setExecutions(response.data);
      setExecutions(mockExecutionHistory);
    } catch (error) {
      console.error('Error loading execution history:', error);
    } finally {
      setLoading(false);
    }
  };

  const columns = [
    {
      title: '执行ID',
      dataIndex: 'id',
      key: 'id',
      width: 150,
      ellipsis: true,
    },
    {
      title: '技能名称',
      dataIndex: 'skillName',
      key: 'skillName',
      sorter: (a, b) => a.skillName.localeCompare(b.skillName),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status) => {
        let color = 'default';
        if (status === 'success') color = 'green';
        else if (status === 'error') color = 'red';
        
        return <Tag color={color}>{status}</Tag>;
      },
    },
    {
      title: '执行时间',
      dataIndex: 'executedAt',
      key: 'executedAt',
      render: (date) => new Date(date).toLocaleString(),
      sorter: (a, b) => new Date(a.executedAt) - new Date(b.executedAt),
    },
    {
      title: '输入',
      dataIndex: 'input',
      key: 'input',
      render: (input) => (
        <pre style={{ 
          margin: 0, 
          padding: '4px', 
          backgroundColor: '#f5f5f5', 
          borderRadius: '2px',
          fontSize: '12px',
          maxHeight: '60px',
          overflow: 'auto'
        }}>
          {JSON.stringify(input, null, 1)}
        </pre>
      ),
    },
    {
      title: '结果',
      dataIndex: 'result',
      key: 'result',
      render: (result) => {
        if (result.error) {
          return (
            <pre style={{ 
              margin: 0, 
              padding: '4px', 
              backgroundColor: '#fff1f0', 
              borderRadius: '2px',
              color: '#ff4d4f',
              fontSize: '12px',
              maxHeight: '60px',
              overflow: 'auto'
            }}>
              {result.error}
            </pre>
          );
        }
        return (
          <pre style={{ 
            margin: 0, 
            padding: '4px', 
            backgroundColor: '#f6ffed', 
            borderRadius: '2px',
            color: '#52c41a',
            fontSize: '12px',
            maxHeight: '60px',
            overflow: 'auto'
          }}>
            {JSON.stringify(result.result || result, null, 1)}
          </pre>
        );
      },
    },
  ];

  return (
    <div>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Title level={2}>执行历史</Title>
        <Space>
          <Button 
            icon={<ReloadOutlined />} 
            onClick={loadExecutionHistory}
            loading={loading}
          >
            刷新
          </Button>
        </Space>
      </div>

      <Card>
        {executions.length > 0 ? (
          <Table 
            dataSource={executions} 
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
        ) : (
          <Empty 
            description="暂无执行历史"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          />
        )}
      </Card>
    </div>
  );
};

export default ExecutionHistoryPage;