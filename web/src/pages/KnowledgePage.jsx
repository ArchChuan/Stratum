import React from 'react';
import {
  Button, Modal, Form, Input, Select, Space, Popconfirm, Tag,
  Typography, Card, Row, Col, Skeleton, Empty, Tooltip,
} from 'antd';
import {
  PlusOutlined, DeleteOutlined, ArrowRightOutlined, BookOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import useKnowledgePage from '../hooks/useKnowledgePage';
import { EMBEDDING_MODEL_OPTIONS } from '../constants';

const { Title, Text, Paragraph } = Typography;

const MODE_COLORS = { hybrid: 'purple', vector: 'blue', graph: 'green' };
const MODE_LABELS = { hybrid: '混合', vector: '向量', graph: '图谱' };

const WorkspaceCard = ({ ws, onDelete, onOpen, isAdmin }) => (
  <Card
    style={{ borderRadius: 12, border: '1px solid #f0f0f0', height: '100%', display: 'flex', flexDirection: 'column', cursor: 'pointer' }}
    styles={{ body: { padding: 20, flex: 1, display: 'flex', flexDirection: 'column' } }}
    hoverable
    onClick={() => onOpen(ws.name)}
  >
    <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 12 }}>
      <div style={{
        width: 40, height: 40, borderRadius: 10,
        background: '#f0f5ff',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        flexShrink: 0,
      }}>
        <BookOutlined style={{ fontSize: 18, color: '#2f54eb' }} />
      </div>
      <Tag
        color={MODE_COLORS[ws.config?.query_mode] || 'default'}
        style={{ border: 'none', borderRadius: 6, fontSize: 11, fontWeight: 500 }}
      >
        {MODE_LABELS[ws.config?.query_mode] || ws.config?.query_mode || '-'}
      </Tag>
    </div>

    <Text strong style={{ fontSize: 15, marginBottom: 4, display: 'block' }}>{ws.name}</Text>
    <Paragraph
      type="secondary"
      ellipsis={{ rows: 2 }}
      style={{ fontSize: 13, marginBottom: 12, flex: 1, marginTop: 0 }}
    >
      {ws.description || '暂无描述'}
    </Paragraph>

    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingTop: 12, borderTop: '1px solid #f5f5f5' }}>
      <Text type="secondary" style={{ fontSize: 12 }}>
        {ws.config?.embedding_model || '-'}
      </Text>
      <div onClick={(e) => e.stopPropagation()}>
        <Space size={4}>
          <Tooltip title="查看详情">
            <Button type="text" size="small" icon={<ArrowRightOutlined />} onClick={() => onOpen(ws.name)} />
          </Tooltip>
          {isAdmin && (
            <Tooltip title="删除">
              <Popconfirm
                title={`确定删除知识库 "${ws.name}"？`}
                onConfirm={() => onDelete(ws.name)}
                okText="删除" okType="danger" cancelText="取消"
              >
                <Button type="text" size="small" danger icon={<DeleteOutlined />} />
              </Popconfirm>
            </Tooltip>
          )}
        </Space>
      </div>
    </div>
  </Card>
);

const KnowledgePage = () => {
  const {
    workspaces, loading, isAdmin,
    createOpen, setCreateOpen, createLoading,
    searchText, setSearchText,
    form, navigate,
    handleCreate, handleDelete,
  } = useKnowledgePage();

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 20 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>知识库</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>管理向量知识空间</Text>
        </div>
        <Space size={8}>
          <Input
            placeholder="搜索知识库..."
            prefix={<SearchOutlined style={{ color: '#bfbfbf' }} />}
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            allowClear
            style={{ width: 200 }}
          />
          {isAdmin && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              新建知识库
            </Button>
          )}
        </Space>
      </div>

      {loading ? (
        <Row gutter={[16, 16]}>
          {[1, 2, 3].map(i => (
            <Col xs={24} sm={12} lg={8} key={i}>
              <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0' }} styles={{ body: { padding: 20 } }}>
                <Skeleton active avatar paragraph={{ rows: 2 }} />
              </Card>
            </Col>
          ))}
        </Row>
      ) : workspaces.length === 0 ? (
        <Empty
          image={<BookOutlined style={{ fontSize: 48, color: '#d9d9d9' }} />}
          description={searchText ? '没有找到匹配的知识库' : '还没有知识库'}
          style={{ padding: '60px 0' }}
        >
          {isAdmin && !searchText && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              创建第一个知识库
            </Button>
          )}
        </Empty>
      ) : (
        <Row gutter={[16, 16]}>
          {workspaces.map(ws => (
            <Col xs={24} sm={12} lg={8} key={ws.name}>
              <WorkspaceCard
                ws={ws}
                onDelete={handleDelete}
                onOpen={(n) => navigate(`/knowledge/${n}`)}
                isAdmin={isAdmin}
              />
            </Col>
          ))}
        </Row>
      )}

      <Modal
        title="新建知识库"
        open={createOpen}
        onCancel={() => { setCreateOpen(false); form.resetFields(); }}
        footer={null}
        destroyOnClose
        width={480}
      >
        <Form form={form} layout="vertical" onFinish={handleCreate}>
          <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入知识库名称' }]}>
            <Input placeholder="仅支持字母、数字、连字符" />
          </Form.Item>
          <Form.Item
            label="描述"
            name="description"
            rules={[{ required: true, message: '请输入知识库描述' }]}
            extra="此描述会直接展示给 AI 模型，请准确描述本知识库的内容范围，以便模型判断是否需要搜索"
          >
            <Input.TextArea rows={3} placeholder="例：包含产品手册、版本说明与常见问题，适用于产品功能咨询" />
          </Form.Item>
          <Form.Item label="嵌入模型" name="embedding_model" initialValue="text-embedding-v3" rules={[{ required: true }]}>
            <Select options={EMBEDDING_MODEL_OPTIONS} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item label="查询模式" name="query_mode" initialValue="hybrid">
            <Select>
              <Option value="hybrid">混合（向量 + 图谱）</Option>
              <Option value="vector">纯向量</Option>
              <Option value="graph">纯图谱</Option>
            </Select>
          </Form.Item>
          <Row gutter={12}>
            <Col span={8}>
              <Form.Item label="分块大小" name="chunk_size" initialValue={512}>
                <Input type="number" min={64} max={2048} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item label="分块重叠" name="chunk_overlap" initialValue={64}>
                <Input type="number" min={0} max={512} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item label="Top-K" name="top_k" initialValue={5}>
                <Input type="number" min={1} max={20} />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item style={{ marginBottom: 0 }}>
            <Button type="primary" htmlType="submit" block loading={createLoading}>创建</Button>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default KnowledgePage;
