import React from 'react';
import {
  Card, Form, InputNumber, Select, Button, Upload, Input,
  Skeleton, Tag, Space, Divider, Typography, Badge,
} from 'antd';
import { SendOutlined, ArrowLeftOutlined, InboxOutlined } from '@ant-design/icons';
import useKnowledgeDetailPage from '../hooks/useKnowledgeDetailPage';

const { Title, Text, Paragraph } = Typography;
const { Option } = Select;
const { TextArea } = Input;

const KnowledgeDetailPage = () => {
  const {
    name, navigate, isAdmin,
    stats, statsLoading,
    configForm, configLoading,
    uploadLoading, queryForm, queryLoading, queryResult,
    handleConfigSave, handleUpload, handleQuery,
  } = useKnowledgeDetailPage();

  if (statsLoading && !stats) {
    return (
      <div>
        <Skeleton active paragraph={{ rows: 1 }} style={{ marginBottom: 20 }} />
        <Card style={{ borderRadius: 12, marginBottom: 16 }}><Skeleton active /></Card>
        <Card style={{ borderRadius: 12 }}><Skeleton active /></Card>
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 20 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/knowledge')} type="text">返回</Button>
        <div>
          <Title level={4} style={{ margin: 0 }}>{name}</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>{stats?.description || '暂无描述'}</Text>
        </div>
      </div>

      {/* Stats overview */}
      <Card style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }} styles={{ body: { padding: '16px 24px' } }}>
        <Space size={32} wrap>
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>嵌入模型</Text>
            <div><Tag>{stats?.config?.embedding_model || '-'}</Tag></div>
          </div>
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>查询模式</Text>
            <div><Tag color="blue">{stats?.config?.query_mode || '-'}</Tag></div>
          </div>
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>向量数</Text>
            <div><Text strong>{stats?.stats?.row_count ?? '—'}</Text></div>
          </div>
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>分块大小</Text>
            <div><Text>{stats?.config?.chunk_size ?? '-'}</Text></div>
          </div>
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>Top-K</Text>
            <div><Text>{stats?.config?.top_k ?? '-'}</Text></div>
          </div>
        </Space>
      </Card>

      {/* Config */}
      {isAdmin && (
        <Card
          title="配置管理"
          style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
        >
          <Form form={configForm} layout="inline" onFinish={handleConfigSave}>
            <Form.Item label="查询模式" name="query_mode">
              <Select style={{ width: 140 }}>
                <Option value="hybrid">混合</Option>
                <Option value="vector">向量</Option>
                <Option value="graph">图谱</Option>
              </Select>
            </Form.Item>
            <Form.Item label="分块大小" name="chunk_size" tooltip="创建后不可修改">
              <InputNumber min={64} max={2048} style={{ width: 100 }} disabled />
            </Form.Item>
            <Form.Item label="分块重叠" name="chunk_overlap" tooltip="创建后不可修改">
              <InputNumber min={0} max={512} style={{ width: 100 }} disabled />
            </Form.Item>
            <Form.Item label="Top-K" name="top_k">
              <InputNumber min={1} max={20} style={{ width: 80 }} />
            </Form.Item>
            <Form.Item>
              <Button type="primary" htmlType="submit" loading={configLoading}>保存</Button>
            </Form.Item>
          </Form>
        </Card>
      )}

      {/* Upload */}
      {isAdmin && (
        <Card
          title="上传文档"
          style={{ borderRadius: 12, border: '1px solid #f0f0f0', marginBottom: 16 }}
        >
          <Upload.Dragger
            beforeUpload={(file) => { handleUpload({ file }); return false; }}
            showUploadList={false}
            accept=".txt,.pdf,.md,.docx"
            style={{ padding: '12px 0' }}
            disabled={uploadLoading}
          >
            <p style={{ fontSize: 32, color: '#bfbfbf', marginBottom: 8 }}><InboxOutlined /></p>
            <p style={{ fontSize: 14, color: '#262626', marginBottom: 4 }}>
              {uploadLoading ? '上传中...' : '点击或拖拽文件到此处上传'}
            </p>
            <p style={{ fontSize: 12, color: '#8c8c8c' }}>支持 .txt .pdf .md .docx，单文件最大 10MB</p>
          </Upload.Dragger>
        </Card>
      )}

      {/* Query */}
      <Card title="查询测试" style={{ borderRadius: 12, border: '1px solid #f0f0f0' }}>
        <Form form={queryForm} onFinish={handleQuery}>
          <Form.Item name="question" rules={[{ required: true, message: '请输入问题' }]}>
            <TextArea rows={3} placeholder="输入查询问题..." />
          </Form.Item>
          <Form.Item style={{ marginBottom: queryResult ? 16 : 0 }}>
            <Button type="primary" htmlType="submit" icon={<SendOutlined />} loading={queryLoading}>
              查询
            </Button>
          </Form.Item>
        </Form>

        {queryResult && (
          <>
            <Divider style={{ margin: '0 0 16px' }} />
            <div style={{ background: '#f6ffed', border: '1px solid #b7eb8f', borderRadius: 10, padding: 16, marginBottom: 12 }}>
              <Text strong style={{ display: 'block', marginBottom: 8, fontSize: 13, color: '#52c41a' }}>回答</Text>
              <Paragraph style={{ margin: 0, lineHeight: 1.7 }}>{queryResult.answer}</Paragraph>
            </div>
            {queryResult.sources?.length > 0 && (
              <div>
                <Text strong style={{ fontSize: 13, display: 'block', marginBottom: 8 }}>
                  来源文档（{queryResult.sources.length}）
                </Text>
                <Space direction="vertical" style={{ width: '100%' }} size={8}>
                  {queryResult.sources.map((s, i) => (
                    <div key={i} style={{ background: '#fafafa', border: '1px solid #f0f0f0', borderRadius: 8, padding: '10px 14px' }}>
                      <Space size={8} style={{ marginBottom: 6 }}>
                        <Tag style={{ margin: 0 }}>文档: {s.document_id?.slice(0, 8)}</Tag>
                        <Badge
                          count={`${(s.score * 100).toFixed(1)}%`}
                          style={{ background: '#52c41a', fontSize: 11 }}
                        />
                      </Space>
                      <Paragraph ellipsis={{ rows: 2 }} type="secondary" style={{ margin: 0, fontSize: 13 }}>
                        {s.content}
                      </Paragraph>
                    </div>
                  ))}
                </Space>
              </div>
            )}
          </>
        )}
      </Card>
    </div>
  );
};

export default KnowledgeDetailPage;
