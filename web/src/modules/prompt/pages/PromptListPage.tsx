import { FileTextOutlined, ReloadOutlined } from '@ant-design/icons';
import { Button, Card, Form, Input, Modal, Pagination, Tag, Typography } from 'antd';
import { useCallback, useState } from 'react';

import { PromptVersionDrawer } from '../components/PromptVersionDrawer';
import { usePromptListPage } from '../hooks/usePromptListPage';
import { PROMPT_STATUS_COLORS, PROMPT_STATUS_LABELS } from '../model/prompt';

import { ResourceListPage } from '@/shared/ui';

interface CreateFormValues {
  key: string;
  content: string;
}

const PROMPT_KEY_PATTERN = /^[a-z][a-z0-9_]*$/;

export const PromptListPage = () => {
  const {
    prompts,
    loading,
    createOpen,
    createLoading,
    total,
    page,
    pageSize,
    pageSizeOptions,
    handlePageChange,
    openCreate,
    closeCreate,
    handleCreate,
    reload,
  } = usePromptListPage();
  const [form] = Form.useForm<CreateFormValues>();
  const [detailKey, setDetailKey] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);

  const openDetail = useCallback((key: string) => {
    setDetailKey(key);
    setDrawerOpen(true);
  }, []);

  const submitCreate = useCallback(async () => {
    let values: CreateFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return; // 校验失败，由 Form 展示错误
    }
    await handleCreate(values.key.trim(), values.content);
    form.resetFields();
  }, [form, handleCreate]);

  return (
    <div>
      <ResourceListPage
        title="提示词管理"
        description="平台级提示词模板，内容寻址版本管理，key 创建后不可变"
        loading={loading}
        items={prompts}
        createLabel="新建模板"
        onCreate={openCreate}
        emptyTitle="提示词还是空的"
        emptyDescription="点击右上角新建第一个提示词模板"
        toolbarExtra={
          <Button icon={<ReloadOutlined />} onClick={reload} loading={loading}>
            刷新
          </Button>
        }
        renderItem={(item) => (
          <Card
            key={item.key}
            hoverable
            size="small"
            style={{ marginBottom: 8 }}
            onClick={() => openDetail(item.key)}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div>
                <FileTextOutlined style={{ marginRight: 8, color: 'rgba(0,0,0,0.45)' }} />
                <Typography.Text strong style={{ marginRight: 8 }}>
                  {item.key}
                </Typography.Text>
                <Tag color={PROMPT_STATUS_COLORS[item.latest_status]}>
                  {PROMPT_STATUS_LABELS[item.latest_status] || item.latest_status}
                </Tag>
                <Typography.Text type="secondary" style={{ fontSize: 12, marginLeft: 8 }}>
                  v{item.latest_version} · {new Date(item.created_at).toLocaleString()}
                </Typography.Text>
              </div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                点击管理版本与 A/B 分流
              </Typography.Text>
            </div>
          </Card>
        )}
      />
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
        <Pagination
          current={page}
          pageSize={pageSize}
          total={total}
          pageSizeOptions={pageSizeOptions}
          showSizeChanger
          showTotal={(t) => `共 ${t} 个模板`}
          onChange={handlePageChange}
        />
      </div>

      <Modal
        title="新建提示词模板"
        open={createOpen}
        confirmLoading={createLoading}
        okText="创建"
        cancelText="取消"
        onOk={() => void submitCreate()}
        onCancel={closeCreate}
        afterClose={() => form.resetFields()}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="key"
            label="模板 Key"
            tooltip="唯一标识（如 system_prompt），创建后不可修改"
            rules={[
              { required: true, message: '请输入模板 Key' },
              {
                pattern: PROMPT_KEY_PATTERN,
                message: '以小写字母开头，仅限小写字母、数字和下划线',
              },
            ]}
          >
            <Input placeholder="如 system_prompt" maxLength={64} />
          </Form.Item>
          <Form.Item
            name="content"
            label="模板内容"
            rules={[{ required: true, message: '请输入模板内容' }]}
          >
            <Input.TextArea rows={6} placeholder="提示词模板内容，支持 {{变量}} 占位符" maxLength={20000} showCount />
          </Form.Item>
        </Form>
      </Modal>

      <PromptVersionDrawer
        templateKey={detailKey}
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        onChanged={reload}
      />
    </div>
  );
};

export default PromptListPage;
