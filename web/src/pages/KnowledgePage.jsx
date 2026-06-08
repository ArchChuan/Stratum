import React, { useEffect, useState } from 'react';
import { Table, Button, Modal, Form, Input, Select, Space, message, Popconfirm, Tag, Typography } from 'antd';
import { PlusOutlined, DeleteOutlined, SettingOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { listWorkspaces, createWorkspace, deleteWorkspace } from '../services/api';
import { useAuth } from '../hooks/useAuth';

const { Title } = Typography;
const { Option } = Select;

const KnowledgePage = () => {
  const [workspaces, setWorkspaces] = useState([]);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [form] = Form.useForm();
  const navigate = useNavigate();
  const { user } = useAuth();

  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  const fetchWorkspaces = async () => {
    setLoading(true);
    try {
      const res = await listWorkspaces();
      setWorkspaces(res.data.workspaces || []);
    } catch (err) {
      message.error(err.response?.data?.error || '获取知识库列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchWorkspaces();
  }, []);

  const handleCreate = async (values) => {
    setCreateLoading(true);
    try {
      await createWorkspace({
        name: values.name,
        description: values.description || '',
        config: {
          embedding_model: values.embedding_model,
          chunk_size: values.chunk_size || 512,
          chunk_overlap: values.chunk_overlap || 64,
          query_mode: values.query_mode || 'hybrid',
          top_k: values.top_k || 5,
        },
      });
      message.success('知识库创建成功');
      setCreateOpen(false);
      form.resetFields();
      fetchWorkspaces();
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '创建失败');
      }
    } finally {
      setCreateLoading(false);
    }
  };

  const handleDelete = async (name) => {
    try {
      await deleteWorkspace(name);
      message.success('知识库已删除');
      fetchWorkspaces();
    } catch (err) {
      if (err.response?.status !== 403) {
        message.error(err.response?.data?.error || '删除失败');
      }
    }
  };

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
      render: (name) => (
        <Button type="link" onClick={() => navigate(`/knowledge/${name}`)}>{name}</Button>
      ),
    },
    { title: '描述', dataIndex: 'description', key: 'description', ellipsis: true },
    {
      title: '嵌入模型',
      key: 'embedding_model',
      render: (_, r) => <Tag>{r.config?.embedding_model}</Tag>,
    },
    {
      title: '查询模式',
      key: 'query_mode',
      render: (_, r) => <Tag color="blue">{r.config?.query_mode}</Tag>,
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      render: (v) => new Date(v).toLocaleString('zh-CN'),
    },
    {
      title: '操作',
      key: 'action',
      render: (_, r) => (
        <Space>
          <Button size="small" icon={<SettingOutlined />} onClick={() => navigate(`/knowledge/${r.name}`)}>
            详情
          </Button>
          {isAdmin && (
            <Popconfirm
              title={`确定删除知识库 "${r.name}"？`}
              onConfirm={() => handleDelete(r.name)}
              okText="删除"
              cancelText="取消"
              okButtonProps={{ danger: true }}
            >
              <Button size="small" danger icon={<DeleteOutlined />}>删除</Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ];

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>知识库管理</Title>
        {isAdmin && (
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
            新建知识库
          </Button>
        )}
      </div>

      <Table
        rowKey="id"
        columns={columns}
        dataSource={workspaces}
        loading={loading}
        pagination={{ pageSize: 10 }}
      />

      <Modal
        title="新建知识库"
        open={createOpen}
        onCancel={() => { setCreateOpen(false); form.resetFields(); }}
        footer={null}
        destroyOnClose
      >
        <Form form={form} layout="vertical" onFinish={handleCreate}>
          <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入知识库名称' }]}>
            <Input placeholder="仅支持字母、数字、连字符" />
          </Form.Item>
          <Form.Item label="描述" name="description">
            <Input.TextArea rows={2} />
          </Form.Item>
          <Form.Item label="嵌入模型" name="embedding_model" initialValue="text-embedding-v3"
            rules={[{ required: true }]}>
            <Select>
              <Option value="text-embedding-v3">text-embedding-v3（千问，1536维）</Option>
              <Option value="embedding-3">embedding-3（智谱，2048维）</Option>
            </Select>
          </Form.Item>
          <Form.Item label="查询模式" name="query_mode" initialValue="hybrid">
            <Select>
              <Option value="hybrid">混合（向量 + 图谱）</Option>
              <Option value="vector">纯向量</Option>
              <Option value="graph">纯图谱</Option>
            </Select>
          </Form.Item>
          <Form.Item label="分块大小" name="chunk_size" initialValue={512}>
            <Input type="number" min={64} max={2048} />
          </Form.Item>
          <Form.Item label="分块重叠" name="chunk_overlap" initialValue={64}>
            <Input type="number" min={0} max={512} />
          </Form.Item>
          <Form.Item label="Top-K" name="top_k" initialValue={5}>
            <Input type="number" min={1} max={20} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={createLoading}>创建</Button>
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
};

export default KnowledgePage;
