import React, { useState, useEffect, useCallback } from 'react';
import { Table, Card, Typography, Tag, Button, Space, message } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { getAgentExecutions } from '../services/api';

const { Title } = Typography;

const statusColor = { success: 'green', error: 'red' };

const columns = [
  {
    title: 'Agent 名称',
    dataIndex: 'agent_name',
    key: 'agent_name',
    sorter: (a, b) => a.agent_name.localeCompare(b.agent_name),
  },
  {
    title: '状态',
    dataIndex: 'status',
    key: 'status',
    render: (s) => <Tag color={statusColor[s] || 'default'}>{s === 'success' ? '成功' : '失败'}</Tag>,
    filters: [
      { text: '成功', value: 'success' },
      { text: '失败', value: 'error' },
    ],
    onFilter: (value, record) => record.status === value,
  },
  {
    title: '输入预览',
    dataIndex: 'input_preview',
    key: 'input_preview',
    ellipsis: true,
  },
  {
    title: '输出预览',
    dataIndex: 'output_preview',
    key: 'output_preview',
    ellipsis: true,
    render: (text, record) =>
      record.status === 'error' ? (
        <span style={{ color: '#ff4d4f' }}>{record.error_message}</span>
      ) : (
        text
      ),
  },
  {
    title: 'Token',
    dataIndex: 'total_tokens',
    key: 'total_tokens',
    width: 90,
    sorter: (a, b) => a.total_tokens - b.total_tokens,
  },
  {
    title: '耗时(ms)',
    dataIndex: 'duration_ms',
    key: 'duration_ms',
    width: 100,
    sorter: (a, b) => a.duration_ms - b.duration_ms,
  },
  {
    title: '时间',
    dataIndex: 'created_at',
    key: 'created_at',
    width: 170,
    render: (d) => new Date(d).toLocaleString('zh-CN'),
    sorter: (a, b) => new Date(a.created_at) - new Date(b.created_at),
    defaultSortOrder: 'descend',
  },
];

const ExecutionHistoryPage = () => {
  const [executions, setExecutions] = useState([]);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await getAgentExecutions();
      setExecutions(res.data.executions || []);
    } catch (err) {
      message.error(err.response?.data?.error || '加载执行历史失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    const run = async () => {
      setLoading(true);
      try {
        const res = await getAgentExecutions();
        if (!cancelled) setExecutions(res.data.executions || []);
      } catch (err) {
        if (!cancelled) message.error(err.response?.data?.error || '加载执行历史失败');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    run();
    return () => { cancelled = true; };
  }, []);

  return (
    <div>
      <div style={{ marginBottom: 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Title level={2}>执行历史</Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={load} loading={loading}>
            刷新
          </Button>
        </Space>
      </div>
      <Card>
        <Table
          dataSource={executions}
          columns={columns}
          rowKey="id"
          loading={loading}
          locale={{ emptyText: '最近 30 天暂无执行记录' }}
          pagination={{
            pageSize: 20,
            showSizeChanger: true,
            showQuickJumper: true,
            showTotal: (total) => `共 ${total} 条`,
          }}
        />
      </Card>
    </div>
  );
};

export default ExecutionHistoryPage;
