import {
  Button,
  Descriptions,
  Drawer,
  Empty,
  Form,
  Select,
  Slider,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useState } from 'react';

import { promptApi } from '../api/prompt.api';
import { PROMPT_STATUS_COLORS, PROMPT_STATUS_LABELS, type PromptBinding, type PromptTemplate } from '../model/prompt';

import { useAuth } from '@/modules/iam';
import { DangerPopconfirm } from '@/shared/ui';

interface RequestError { response?: { data?: { error?: string } } }

interface PromptVersionDrawerProps {
  /** 当前打开的模板 key；null 时抽屉不渲染内容。 */
  templateKey: string | null;
  open: boolean;
  onClose: () => void;
  /** 版本发布成功后回调（父级刷新摘要列表的 latest_status）。 */
  onChanged?: () => void;
}

interface BindingFormValues {
  stable_version_id: string;
  canary_version_id?: string;
  traffic_percent: number;
}

/** A/B 流量阈值，绑定版本选择下拉的最大选项数。 */
const VERSION_SELECT_LIMIT = 50;

export const PromptVersionDrawer = ({ templateKey, open, onClose, onChanged }: PromptVersionDrawerProps) => {
  const { user } = useAuth();
  const tenantID = user?.tenant_id ?? user?.current_tenant?.id ?? '';
  const [versions, setVersions] = useState<PromptTemplate[]>([]);
  const [bindings, setBindings] = useState<PromptBinding[]>([]);
  const [loading, setLoading] = useState(false);
  const [publishing, setPublishing] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm<BindingFormValues>();

  const load = useCallback(async () => {
    if (!templateKey) return;
    setLoading(true);
    try {
      const [versionList, bindingList] = await Promise.all([
        promptApi.listVersions(templateKey),
        promptApi.listBindings(),
      ]);
      setVersions(versionList);
      setBindings(bindingList.filter((b) => b.key === templateKey));
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '加载模板详情失败', duration: 0 });
    } finally {
      setLoading(false);
    }
  }, [templateKey]);

  useEffect(() => {
    if (open && templateKey) {
      setBindings([]);
      form.resetFields();
      void load();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- open 打开时重置并加载
  }, [open, templateKey, load]);

  const handlePublish = useCallback(async (version: number) => {
    if (!templateKey) return;
    setPublishing(`v${version}`);
    try {
      await promptApi.publishVersion(templateKey, version);
      message.success({ content: `v${version} 已发布`, duration: 2 });
      await load();
      onChanged?.();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '发布失败', duration: 0 });
    } finally {
      setPublishing(null);
    }
  }, [templateKey, load, onChanged]);

  const versionOptions = versions
    .slice(0, VERSION_SELECT_LIMIT)
    .map((v) => ({ value: v.content_hash, label: `v${v.version} · ${v.content_hash.slice(0, 8)}` }));

  const handleSaveBinding = useCallback(async () => {
    if (!templateKey || !tenantID) return;
    const values = await form.validateFields();
    setSaving(true);
    try {
      await promptApi.upsertBinding({
        key: templateKey,
        scope: `tenant:${tenantID}`,
        stable_version_id: values.stable_version_id,
        canary_version_id: values.canary_version_id || '',
        traffic_percent: values.traffic_percent,
      });
      message.success({ content: 'A/B 绑定已保存', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '保存 A/B 绑定失败', duration: 0 });
    } finally {
      setSaving(false);
    }
  }, [form, load, templateKey, tenantID]);

  const handleDeleteBinding = useCallback(async (scope: string) => {
    if (!templateKey) return;
    try {
      await promptApi.deleteBinding(templateKey, scope);
      message.success({ content: 'A/B 绑定已清除', duration: 2 });
      await load();
    } catch (err) {
      message.error({ content: (err as RequestError).response?.data?.error || '清除 A/B 绑定失败', duration: 0 });
    }
  }, [load, templateKey]);

  const columns: ColumnsType<PromptTemplate> = [
    { title: '版本', dataIndex: 'version', width: 72, render: (v: number) => `v${v}` },
    {
      title: '状态',
      dataIndex: 'status',
      width: 96,
      render: (status: string) => <Tag color={PROMPT_STATUS_COLORS[status]}>{PROMPT_STATUS_LABELS[status] || status}</Tag>,
    },
    {
      title: '内容摘要',
      dataIndex: 'content',
      ellipsis: true,
      render: (content: string) => <Typography.Text style={{ fontSize: 12 }}>{content.slice(0, 80)}</Typography.Text>,
    },
    { title: '创建时间', dataIndex: 'created_at', width: 150, render: (v: string) => new Date(v).toLocaleString() },
    {
      title: '操作',
      key: 'actions',
      width: 96,
      render: (_, record) => (
        <Button
          type="link"
          size="small"
          disabled={record.status === 'published'}
          loading={publishing === `v${record.version}`}
          onClick={() => void handlePublish(record.version)}
        >
          {record.status === 'published' ? '已发布' : '发布'}
        </Button>
      ),
    },
  ];

  const currentBinding = bindings.find((b) => b.scope === `tenant:${tenantID}`);

  return (
    <Drawer
      title={templateKey ? `提示词模板 · ${templateKey}` : '提示词模板'}
      open={open}
      width={640}
      onClose={onClose}
    >
      {!templateKey ? null : (
        <Space direction="vertical" style={{ width: '100%' }} size="large">
          <section>
            <Typography.Title level={5}>版本历史（内容寻址，发布后不可变）</Typography.Title>
            <Table<PromptTemplate>
              rowKey={(v) => `${v.version}`}
              columns={columns}
              dataSource={versions}
              loading={loading}
              size="small"
              pagination={false}
            />
          </section>

          <section>
            <Typography.Title level={5}>
              A/B 分流
              <Typography.Text type="secondary" style={{ fontSize: 12, marginLeft: 8 }}>
                按请求 ID 哈希分流，canary 为实验版本
              </Typography.Text>
            </Typography.Title>
            {bindings.length > 0 && (
              <div style={{ marginBottom: 12 }}>
                {bindings.map((b) => (
                  <div
                    key={b.scope}
                    style={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      padding: '8px 12px',
                      border: '1px solid #f0f0f0',
                      borderRadius: 6,
                      marginBottom: 8,
                    }}
                  >
                    <div>
                      <Typography.Text code>{b.scope}</Typography.Text>
                      <div style={{ fontSize: 12, color: 'rgba(0,0,0,0.45)', marginTop: 4 }}>
                        stable <Tag color="green">{b.stable_version_id.slice(0, 8)}</Tag>
                        {b.canary_version_id && (
                          <>
                            canary <Tag color="blue">{b.canary_version_id.slice(0, 8)}</Tag>
                          </>
                        )}
                        <Typography.Text>分流 {b.traffic_percent}%</Typography.Text>
                      </div>
                    </div>
                    <DangerPopconfirm
                      title="清除该 A/B 绑定？"
                      description="清除后该模板恢复为稳定版本直发。"
                      onConfirm={() => void handleDeleteBinding(b.scope)}
                    >
                      <Button type="link" danger size="small">清除</Button>
                    </DangerPopconfirm>
                  </div>
                ))}
              </div>
            )}
            {currentBinding && (
              <Descriptions size="small" column={1} style={{ marginBottom: 12 }}>
                <Descriptions.Item label="当前租户绑定">
                  <Tag color="green">{currentBinding.stable_version_id.slice(0, 8)}</Tag>
                  {currentBinding.canary_version_id && <Tag color="blue">{currentBinding.canary_version_id.slice(0, 8)}</Tag>}
                </Descriptions.Item>
              </Descriptions>
            )}
            <Form
              form={form}
              layout="vertical"
              size="small"
              initialValues={{ traffic_percent: 0 }}
              onFinish={() => void handleSaveBinding()}
            >
              <Form.Item
                name="stable_version_id"
                label="稳定版本"
                rules={[{ required: true, message: '请选择稳定版本' }]}
              >
                <Select
                  placeholder="选择稳定版本（内容哈希）"
                  options={versionOptions}
                  showSearch
                  optionFilterProp="label"
                />
              </Form.Item>
              <Form.Item name="canary_version_id" label="实验版本（canary，可选）">
                <Select
                  placeholder="选择实验版本，流量按下方比例分流"
                  options={[{ value: '', label: '不使用实验版本' }, ...versionOptions]}
                  allowClear
                  onChange={(v?: string) => form.setFieldValue('canary_version_id', v || '')}
                />
              </Form.Item>
              <Form.Item name="traffic_percent" label="实验流量比例">
                <Slider min={0} max={100} marks={{ 0: '0%', 50: '50%', 100: '100%' }} tooltip={{ formatter: (v) => `${v}%` }} />
              </Form.Item>
              <Button type="primary" htmlType="submit" loading={saving} block>
                {currentBinding ? '更新 A/B 绑定' : '新建 A/B 绑定'}
              </Button>
            </Form>
            {bindings.length === 0 && !loading && (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚无 A/B 绑定" style={{ margin: '8px 0' }} />
            )}
          </section>
        </Space>
      )}
    </Drawer>
  );
};

export default PromptVersionDrawer;
