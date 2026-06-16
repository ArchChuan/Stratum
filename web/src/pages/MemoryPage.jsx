import React from 'react';
import {
  Card, Table, Button, Input, Modal, Space, Tag, Select,
  Popconfirm, Tabs, Empty, Descriptions, Tooltip, Typography,
} from 'antd';
import {
  DeleteOutlined, SearchOutlined, PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import useMemoryPage from '../hooks/useMemoryPage';
import { COMPACT_PAGE_SIZE } from '../constants';

const { Search, TextArea } = Input;
const { Title, Text } = Typography;

const importanceColor = (v) => v > 0.7 ? '#f5222d' : v > 0.5 ? '#fa8c16' : '#52c41a';

const memoryColumns = (onDelete) => [
  {
    title: '类型', dataIndex: 'type', key: 'type', width: 80,
    render: (type) => (
      <Tag color={type === 'user' ? 'blue' : type === 'assistant' ? 'green' : 'default'}>
        {type === 'user' ? '用户' : type === 'assistant' ? '助手' : '系统'}
      </Tag>
    ),
  },
  {
    title: '内容', dataIndex: 'content', key: 'content', ellipsis: true,
    render: (content) => <Tooltip title={content} placement="topLeft"><Text ellipsis>{content}</Text></Tooltip>,
  },
  {
    title: '标签', dataIndex: 'tags', key: 'tags', width: 160,
    render: (tags) => tags?.map(tag => <Tag key={tag} style={{ marginBottom: 2 }}>{tag}</Tag>),
  },
  {
    title: '重要性', dataIndex: 'importance', key: 'importance', width: 80, align: 'right',
    sorter: (a, b) => (a.importance || 0) - (b.importance || 0),
    render: (v) => <Text style={{ color: importanceColor(v), fontWeight: 600 }}>{v?.toFixed(2) ?? '-'}</Text>,
  },
  {
    title: '时间', dataIndex: 'timestamp', key: 'timestamp', width: 150,
    render: (ts) => <Text type="secondary">{new Date(ts).toLocaleString('zh-CN')}</Text>,
  },
  {
    title: '操作', key: 'action', width: 80,
    render: (_, record) => (
      <Popconfirm title="确定删除这条记忆？" onConfirm={() => onDelete(record.id)} okText="删除" cancelText="取消">
        <Button danger size="small" icon={<DeleteOutlined />} type="text" />
      </Popconfirm>
    ),
  },
];

const MemoryPage = () => {
  const {
    searchQuery, setSearchQuery, searchResults, loading,
    stats, entities, summary,
    sessionIdInput, setSessionIdInput,
    createOpen, setCreateOpen, newMemory, setNewMemory,
    handleSearch, handleAddMemory, handleDeleteMemory,
    loadEntities, loadSummary, resetNewMemory,
  } = useMemoryPage();

  const searchData = searchResults.map((r, i) => ({
    key: r.entry?.id || i,
    id: r.entry?.id,
    type: r.entry?.role || 'system',
    content: r.entry?.content || '',
    tags: r.entry?.tags || [],
    importance: r.entry?.importance,
    timestamp: r.entry?.timestamp,
    score: r.score,
  }));

  return (
    <div style={{ padding: 24 }}>
      <Title level={3}>记忆管理</Title>
      <Tabs defaultActiveKey="search" items={[
        { key: 'search', label: '搜索', children: (
          <Space direction="vertical" style={{ width: '100%' }} size="middle">
            <Space>
              <Search placeholder="搜索记忆..." value={searchQuery} onChange={e => setSearchQuery(e.target.value)}
                onSearch={handleSearch} enterButton={<><SearchOutlined /> 搜索</>} style={{ width: 400 }} loading={loading} allowClear />
              <Button icon={<PlusOutlined />} type="primary" onClick={() => setCreateOpen(true)}>添加记忆</Button>
            </Space>
            {searchData.length > 0
              ? <Table dataSource={searchData} columns={memoryColumns(handleDeleteMemory)} pagination={{ pageSize: COMPACT_PAGE_SIZE }} size="small" />
              : <Empty description="输入关键词搜索记忆" />}
          </Space>
        )},
        { key: 'stats', label: '统计', children: stats ? (
          <Card size="small">
            <Descriptions column={3} bordered size="small">
              <Descriptions.Item label="总记忆数">{stats.total_entries ?? 0}</Descriptions.Item>
              <Descriptions.Item label="实体数">{stats.total_entities ?? 0}</Descriptions.Item>
              <Descriptions.Item label="关系数">{stats.total_relations ?? 0}</Descriptions.Item>
              <Descriptions.Item label="会话数">{stats.total_sessions ?? 0}</Descriptions.Item>
              <Descriptions.Item label="向量数">{stats.vector_count ?? 0}</Descriptions.Item>
              <Descriptions.Item label="富化数">{stats.enriched_count ?? 0}</Descriptions.Item>
            </Descriptions>
          </Card>
        ) : <Empty description="暂无统计数据" /> },
        { key: 'entities', label: '实体图谱', children: (
          <Space direction="vertical" style={{ width: '100%' }}>
            <Button icon={<ReloadOutlined />} onClick={loadEntities}>加载实体</Button>
            {entities.length > 0
              ? <Space wrap>{entities.map(e => <Tag key={e.name} color="blue">{e.name} ({e.type})</Tag>)}</Space>
              : <Empty description="点击加载实体" />}
          </Space>
        )},
        { key: 'summary', label: '摘要', children: (
          <Space direction="vertical" style={{ width: '100%' }}>
            <Space>
              <Input value={sessionIdInput} onChange={e => setSessionIdInput(e.target.value)} placeholder="输入会话 ID" style={{ width: 300 }} />
              <Button onClick={loadSummary} type="primary">生成摘要</Button>
            </Space>
            {summary && <Card size="small"><Text>{summary}</Text></Card>}
          </Space>
        )},
      ]} />

      <Modal title="添加记忆" open={createOpen} onOk={handleAddMemory} onCancel={resetNewMemory} okText="添加" cancelText="取消">
        <Space direction="vertical" style={{ width: '100%' }} size="middle">
          <Select value={newMemory.role} onChange={v => setNewMemory(m => ({ ...m, role: v }))} style={{ width: '100%' }}
            options={[{ value: 'user', label: '用户' }, { value: 'assistant', label: '助手' }, { value: 'system', label: '系统' }]} />
          <TextArea rows={4} value={newMemory.content} onChange={e => setNewMemory(m => ({ ...m, content: e.target.value }))} placeholder="记忆内容..." />
          <Select mode="tags" value={newMemory.tags} onChange={v => setNewMemory(m => ({ ...m, tags: v }))} placeholder="添加标签" style={{ width: '100%' }} />
        </Space>
      </Modal>
    </div>
  );
};

export default MemoryPage;
