import React, { useEffect, useState } from 'react';
import { Card, Descriptions, Form, InputNumber, Select, Button, Upload, Input, message, Spin, Typography, Divider, Tag, Space } from 'antd';
import { UploadOutlined, SendOutlined, ArrowLeftOutlined } from '@ant-design/icons';
import { useParams, useNavigate } from 'react-router-dom';
import { getWorkspaceStats, updateWorkspace, ingestDocument, queryKnowledge } from '../services/api';
import { useAuth } from '../hooks/useAuth';

const { Title, Paragraph } = Typography;
const { Option } = Select;
const { TextArea } = Input;

const KnowledgeDetailPage = () => {
  const { name } = useParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin' || user?.role === 'owner';

  const [stats, setStats] = useState(null);
  const [statsLoading, setStatsLoading] = useState(false);
  const [configForm] = Form.useForm();
  const [configLoading, setConfigLoading] = useState(false);
  const [uploadLoading, setUploadLoading] = useState(false);
  const [queryForm] = Form.useForm();
  const [queryLoading, setQueryLoading] = useState(false);
  const [queryResult, setQueryResult] = useState(null);

  const fetchStats = async () => {
    setStatsLoading(true);
    try {
      const res = await getWorkspaceStats(name);
      setStats(res.data);
      configForm.setFieldsValue({
        chunk_size: res.data.config?.chunk_size,
        chunk_overlap: res.data.config?.chunk_overlap,
        query_mode: res.data.config?.query_mode,
        top_k: res.data.config?.top_k,
      });
    } catch (err) {
      message.error(err.response?.data?.error || '获取知识库详情失败');
    } finally {
      setStatsLoading(false);
    }
  };

  useEffect(() => {
    fetchStats();
  }, [name]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleConfigSave = async (values) => {
    setConfigLoading(true);
    try {
      await updateWorkspace(name, {
        config: {
          embedding_model: stats?.config?.embedding_model,
          chunk_size: values.chunk_size,
          chunk_overlap: values.chunk_overlap,
          query_mode: values.query_mode,
          top_k: values.top_k,
        },
      });
      message.success('配置已保存');
      fetchStats();
    } catch (err) {
      message.error(err.response?.data?.error || '保存失败');
    } finally {
      setConfigLoading(false);
    }
  };

  const handleUpload = async ({ file }) => {
    const formData = new FormData();
    formData.append('workspace', name);
    formData.append('file', file);
    setUploadLoading(true);
    try {
      const res = await ingestDocument(formData);
      message.success(`上传成功，共 ${res.data.total_chunks} 个分块`);
      fetchStats();
    } catch (err) {
      message.error(err.response?.data?.error || '上传失败');
    } finally {
      setUploadLoading(false);
    }
    return false;
  };

  const handleQuery = async (values) => {
    setQueryLoading(true);
    setQueryResult(null);
    try {
      const res = await queryKnowledge({
        question: values.question,
        workspace: name,
        mode: values.mode || stats?.config?.query_mode || 'hybrid',
        topK: values.top_k || stats?.config?.top_k || 5,
      });
      setQueryResult(res.data);
    } catch (err) {
      message.error(err.response?.data?.error || '查询失败');
    } finally {
      setQueryLoading(false);
    }
  };

  if (statsLoading && !stats) {
    return <Spin style={{ margin: '80px auto', display: 'block' }} />;
  }

  return (
    <>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/knowledge')}>返回列表</Button>
        <Title level={4} style={{ margin: 0 }}>{name}</Title>
      </Space>

      <Descriptions bordered size="small" style={{ marginBottom: 16 }}>
        <Descriptions.Item label="描述">{stats?.description || '—'}</Descriptions.Item>
        <Descriptions.Item label="嵌入模型">
          <Tag>{stats?.config?.embedding_model}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="向量数量">{stats?.stats?.row_count ?? '—'}</Descriptions.Item>
      </Descriptions>

      {isAdmin && (
        <Card title="配置管理" style={{ marginBottom: 16 }}>
          <Form form={configForm} layout="inline" onFinish={handleConfigSave}>
            <Form.Item label="查询模式" name="query_mode">
              <Select style={{ width: 140 }}>
                <Option value="hybrid">混合</Option>
                <Option value="vector">向量</Option>
                <Option value="graph">图谱</Option>
              </Select>
            </Form.Item>
            <Form.Item label="分块大小" name="chunk_size">
              <InputNumber min={64} max={2048} />
            </Form.Item>
            <Form.Item label="分块重叠" name="chunk_overlap">
              <InputNumber min={0} max={512} />
            </Form.Item>
            <Form.Item label="Top-K" name="top_k">
              <InputNumber min={1} max={20} />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={configLoading}>保存</Button>
            </Form.Item>
          </Form>
        </Card>
      )}

      {isAdmin && (
        <Card title="上传文档" style={{ marginBottom: 16 }}>
          <Upload
            beforeUpload={(file) => { handleUpload({ file }); return false; }}
            showUploadList={false}
            accept=".txt,.pdf,.md,.docx"
          >
            <Button icon={<UploadOutlined />} loading={uploadLoading}>选择文件上传</Button>
          </Upload>
          <Paragraph type="secondary" style={{ marginTop: 8 }}>
            支持 .txt .pdf .md .docx，单文件最大 10MB
          </Paragraph>
        </Card>
      )}

      <Card title="查询测试">
        <Form form={queryForm} onFinish={handleQuery}>
          <Form.Item name="question" rules={[{ required: true, message: '请输入问题' }]}>
            <TextArea rows={3} placeholder="输入查询问题..." />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" icon={<SendOutlined />} loading={queryLoading}>
              查询
            </Button>
          </Form.Item>
        </Form>

        {queryResult && (
          <>
            <Divider>查询结果</Divider>
            <Card size="small" title="回答" style={{ marginBottom: 8 }}>
              <Paragraph>{queryResult.answer}</Paragraph>
            </Card>
            {queryResult.sources?.length > 0 && (
              <Card size="small" title={`来源文档（${queryResult.sources.length} 个）`}>
                {queryResult.sources.map((s, i) => (
                  <Card.Grid key={i} style={{ width: '100%', padding: '8px 12px' }}>
                    <Tag>文档: {s.document_id?.slice(0, 8)}</Tag>
                    <Tag color="green">分数: {s.score?.toFixed(3)}</Tag>
                    <Paragraph type="secondary" ellipsis={{ rows: 2 }} style={{ marginTop: 4, marginBottom: 0 }}>
                      {s.content}
                    </Paragraph>
                  </Card.Grid>
                ))}
              </Card>
            )}
          </>
        )}
      </Card>
    </>
  );
};

export default KnowledgeDetailPage;
