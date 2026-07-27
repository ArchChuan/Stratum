import { ArrowLeftOutlined, CheckOutlined, CloseOutlined, SaveOutlined } from '@ant-design/icons';
import { Alert, Button, Descriptions, Empty, Form, Input, InputNumber, Modal, Skeleton, Space, Table, Tag, Timeline, Typography } from 'antd';
import { useMemo } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { useResourceChangeProposal } from '../hooks/useResourceChangeProposal';
import { TERMINAL_PROPOSAL_STATUSES, type ProposalPayload } from '../model/proposal';

const { Title, Text } = Typography;
const KIND_LABEL = { agent: 'Agent', skill_draft: 'Skill 草稿', mcp_config: 'MCP 配置', knowledge_workspace: '知识库' };
const STATUS: Record<string, { label: string; color: string; note: string }> = {
  ready_for_review: { label: '待审阅', color: 'blue', note: '确认后将立即应用到当前租户。' },
  applied: { label: '已应用', color: 'green', note: '变更已完成并通过安全读回。' },
  stale: { label: '基线冲突', color: 'orange', note: '目标资源已变化，本提案不能继续应用。' },
  expired: { label: '已过期', color: 'default', note: '审阅期限已结束，请重新生成提案。' },
  failed: { label: '应用失败', color: 'red', note: '变更未完成，系统不会自动重试。' },
  unknown_outcome: { label: '结果未知', color: 'red', note: '外部写入结果无法确认。为避免重复写入，本提案禁止重试。' },
  invalid: { label: '无效', color: 'red', note: '提案未通过严格字段校验。' },
  cancelled: { label: '已取消', color: 'default', note: '提案已由管理员取消。' },
  confirmed: { label: '已确认', color: 'processing', note: '提案已确认，等待应用。' },
  applying: { label: '应用中', color: 'processing', note: '系统正在应用变更。' },
  draft: { label: '草稿', color: 'default', note: '提案仍在准备中。' },
};

const FIELD_LABEL: Record<string, string> = {
  name: '名称', description: '说明', model: '模型', maxIterations: '最大迭代次数',
  maxContextTokens: '上下文上限', skillIds: 'Skill', mcpToolIds: 'MCP 工具', workspaceIds: '知识库',
  instructions: '指令', version: '版本', transport: '传输方式', command: '命令', args: '参数',
  url: '地址', capabilities: '能力', timeoutSec: '超时（秒）', retry: '重试策略', embeddingModel: '向量模型',
};
const ARRAY_FIELDS = new Set(['skillIds', 'mcpToolIds', 'workspaceIds', 'args', 'capabilities']);
const NUMBER_FIELDS = new Set(['maxIterations', 'maxContextTokens', 'timeoutSec']);
const formatValue = (value: unknown) => Array.isArray(value) ? value.join('、') : typeof value === 'object' ? JSON.stringify(value) : String(value ?? '');

export const ResourceChangeProposalPage = () => {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const { proposal, events = [], loading, saving, confirming, canceling, saveDraft, confirm, cancel } = useResourceChangeProposal(id);
  const [form] = Form.useForm<Record<string, unknown>>();
  const rows = useMemo(() => {
    if (!proposal) return [];
    const baseline = proposal.baselineProjection ?? {};
    return Object.entries(proposal.payload).map(([field, value]) => ({ field, oldValue: baseline[field], value }));
  }, [proposal]);

  if (loading) return <Skeleton active paragraph={{ rows: 8 }} />;
  if (!proposal) return <Empty description="没有找到这条变更提案" />;

  const status = STATUS[proposal.status] || { label: proposal.status, color: 'default', note: '状态已更新。' };
  const editable = proposal.status === 'ready_for_review' && !TERMINAL_PROPOSAL_STATUSES.has(proposal.status);
  const initialValues = Object.fromEntries(Object.entries(proposal.payload).map(([key, value]) => [key, ARRAY_FIELDS.has(key) ? (value as string[] | undefined)?.join(', ') : value]));
  const submit = (values: Record<string, unknown>) => {
    const payload = { ...proposal.payload } as Record<string, unknown>;
    Object.entries(values).forEach(([key, value]) => {
      payload[key] = ARRAY_FIELDS.has(key)
        ? String(value ?? '').split(',').map((item) => item.trim()).filter(Boolean)
        : value;
    });
    void saveDraft(payload as ProposalPayload);
  };

  return (
    <div style={{ maxWidth: 1080, margin: '0 auto', minWidth: 0 }}>
      <Button aria-label="返回" type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate(-1)} style={{ marginBottom: 12 }}>返回</Button>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16, flexWrap: 'wrap', borderBottom: '1px solid #e8e8e8', paddingBottom: 18 }}>
        <div style={{ minWidth: 0 }}>
          <Space wrap size={8}><Title level={3} style={{ margin: 0 }}>审阅资源变更</Title><Tag color={status.color}>{status.label}</Tag></Space>
          <Text type="secondary">{KIND_LABEL[proposal.resourceKind]} · {proposal.operation === 'create' ? '新建' : '更新'} · {proposal.resourceId || '新资源'}</Text>
        </div>
        {editable && <Space wrap>
          <Button aria-label="取消提案" danger icon={<CloseOutlined />} loading={canceling} onClick={() => Modal.confirm({
            title: '确认取消这条提案？', content: '取消后不能恢复，也不会修改目标资源。', okText: '取消提案', okButtonProps: { danger: true }, cancelText: '返回', onOk: cancel,
          })}>取消提案</Button>
          <Button aria-label="确认并应用" type="primary" icon={<CheckOutlined />} loading={confirming} disabled={saving} onClick={() => Modal.confirm({
            title: '确认应用这次变更？', content: '系统会再次检查权限和资源基线，然后只执行一次写入。', okText: '确认并应用', cancelText: '继续审阅', onOk: confirm,
          })}>确认并应用</Button>
        </Space>}
      </header>

      <Alert style={{ marginBlock: 18 }} type={proposal.status === 'ready_for_review' ? 'warning' : proposal.status === 'applied' ? 'success' : 'info'} showIcon message={status.note} />

      <Descriptions title="提案信息" size="small" column={{ xs: 1, sm: 2, lg: 3 }}>
        <Descriptions.Item label="提案编号">{proposal.id}</Descriptions.Item>
        <Descriptions.Item label="发起人">{proposal.proposerId}</Descriptions.Item>
        <Descriptions.Item label="有效期">{new Date(proposal.expiresAt).toLocaleString('zh-CN')}</Descriptions.Item>
        <Descriptions.Item label="影响范围">当前租户的单个 {KIND_LABEL[proposal.resourceKind]} 资源</Descriptions.Item>
        <Descriptions.Item label="所需权限">租户管理员或所有者</Descriptions.Item>
        <Descriptions.Item label="基线状态">{proposal.operation === 'create' ? '不适用（新建）' : proposal.baselineFingerprint ? '已记录，确认时重新校验' : '缺少基线'}</Descriptions.Item>
      </Descriptions>

      <section style={{ marginTop: 24, minWidth: 0 }}>
        <Title level={4}>字段变更</Title>
        <Table size="small" pagination={false} rowKey="field" scroll={{ x: 520 }} dataSource={rows} columns={[
          { title: '字段', dataIndex: 'field', width: 180, render: (field: string) => FIELD_LABEL[field] || field },
          { title: '当前值', dataIndex: 'oldValue', render: (value: unknown) => proposal.operation === 'create' ? '无（新建）' : formatValue(value) },
          { title: '提议值', dataIndex: 'value', render: formatValue },
          { title: '影响', key: 'impact', width: 220, render: () => proposal.operation === 'create' ? '创建新的租户资源' : '通过基线校验后覆盖该字段' },
        ]} />
      </section>

      {editable && <section style={{ marginTop: 24 }}>
        <Title level={4}>调整提案</Title>
        <Form form={form} layout="vertical" initialValues={initialValues} onFinish={submit}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(260px, 100%), 1fr))', gap: '0 16px' }}>
            {rows.filter(({ field }) => field !== 'retry').map(({ field }) => (
              <Form.Item key={field} name={field} label={FIELD_LABEL[field] || field} rules={[{ required: field === 'name', message: '请输入名称' }]}>
                {NUMBER_FIELDS.has(field) ? <InputNumber style={{ width: '100%' }} /> : field === 'instructions' || field === 'description' ? <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} /> : <Input />}
              </Form.Item>
            ))}
          </div>
          <Button aria-label="保存调整" htmlType="submit" icon={<SaveOutlined />} loading={saving} disabled={confirming}>保存调整</Button>
        </Form>
      </section>}

      <section style={{ marginTop: 28 }}>
        <Title level={4}>状态记录</Title>
        {events.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="状态记录还是空的" /> : <Timeline items={events.map((event) => ({
          color: event.toStatus === 'applied' ? 'green' : event.toStatus === 'failed' || event.toStatus === 'unknown_outcome' ? 'red' : 'blue',
          children: <div><Text strong>{STATUS[event.toStatus]?.label || event.toStatus}</Text>{event.summary && <Text style={{ display: 'block' }}>{event.summary}</Text>}<Text type="secondary" style={{ fontSize: 12 }}>{new Date(event.createdAt).toLocaleString('zh-CN')}</Text></div>,
        }))} />}
      </section>
    </div>
  );
};
