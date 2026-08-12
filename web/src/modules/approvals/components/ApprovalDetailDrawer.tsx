import { Button, Descriptions, Drawer, Space, Tag } from 'antd';
import { useCallback } from 'react';

import type { ApprovalDetail } from '../api';
import { riskLevelLabel, statusLabel, subjectKindLabel } from '../labels';

// 键名全小写存储，匹配时对 key 做 toLowerCase，保证 apiKey/api_key 等大小写变体都能命中。
const SENSITIVE_PAYLOAD_KEYS = ['apikey', 'api_key', 'token', 'secret'];

/** payload 中命中敏感键名的值用掩码展示，避免工作台详情把凭据原样带出。 */
const maskPayload = (payload: Record<string, unknown> | undefined): Record<string, unknown> => {
  if (!payload) return {};
  const masked: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(payload)) {
    const sensitive = SENSITIVE_PAYLOAD_KEYS.some((k) => key.toLowerCase().includes(k));
    masked[key] = sensitive ? '******' : value;
  }
  return masked;
};

interface ApprovalDetailDrawerProps {
  detail: ApprovalDetail | null;
  loading: boolean;
  open: boolean;
  executeLoading: boolean;
  onExecute: (id: string) => void;
  onClose: () => void;
}

export const ApprovalDetailDrawer = ({
  detail,
  loading,
  open,
  executeLoading,
  onExecute,
  onClose,
}: ApprovalDetailDrawerProps) => {
  // 执行按钮需同时满足已批准且未过期；过期后按钮隐藏，避免展示必然失败的动作。
  // 后端执行路径仍独立校验（fail closed），此处仅为展示层纵深防御。
  const canExecute =
    detail?.status === 'approved' &&
    !!detail.expires_at &&
    new Date(detail.expires_at).getTime() > Date.now();

  const handleExecute = useCallback(() => {
    if (!detail) return;
    onExecute(detail.id);
  }, [detail, onExecute]);

  const payloadEntries = Object.entries(maskPayload(detail?.payload));

  return (
    <Drawer
      title="审批详情"
      width={520}
      open={open}
      loading={loading}
      onClose={onClose}
    >
      {detail && (
        <>
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label="状态">
              <Tag color={detail.status === 'approved' ? 'green' : 'default'}>
                {statusLabel(detail.status)}
              </Tag>
            </Descriptions.Item>
            <Descriptions.Item label="类型">{subjectKindLabel(detail.subject_kind)}</Descriptions.Item>
            <Descriptions.Item label="工具/资源">{detail.tool_name}</Descriptions.Item>
            <Descriptions.Item label="服务 ID">{detail.server_id}</Descriptions.Item>
            <Descriptions.Item label="风险等级">
              <Tag color={detail.risk_level === 'destructive' ? 'red' : 'blue'}>
                {riskLevelLabel(detail.risk_level)}
              </Tag>
            </Descriptions.Item>
            <Descriptions.Item label="发起人">{detail.user_id}</Descriptions.Item>
            <Descriptions.Item label="指派审批人">{detail.assigned_approver || '未指派'}</Descriptions.Item>
            <Descriptions.Item label="创建时间">
              {new Date(detail.created_at).toLocaleString()}
            </Descriptions.Item>
            <Descriptions.Item label="过期时间">
              {new Date(detail.expires_at).toLocaleString()}
            </Descriptions.Item>
            {detail.decided_by && (
              <Descriptions.Item label="处理人">{detail.decided_by}</Descriptions.Item>
            )}
            {detail.decision_reason && (
              <Descriptions.Item label="处理原因">{detail.decision_reason}</Descriptions.Item>
            )}
            {detail.invalidation_reason && (
              <Descriptions.Item label="失效原因">{detail.invalidation_reason}</Descriptions.Item>
            )}
          </Descriptions>

          {payloadEntries.length > 0 && (
            <Descriptions
              column={1}
              size="small"
              bordered
              title="请求参数"
              style={{ marginTop: 16 }}
            >
              {payloadEntries.map(([key, value]) => (
                <Descriptions.Item key={key} label={key}>
                  {typeof value === 'string' ? value : JSON.stringify(value)}
                </Descriptions.Item>
              ))}
            </Descriptions>
          )}

          <Space style={{ marginTop: 24 }}>
            {canExecute && (
              <Button
                type="primary"
                danger
                loading={executeLoading}
                onClick={handleExecute}
              >
                执行
              </Button>
            )}
            <Button onClick={onClose}>关闭</Button>
          </Space>
        </>
      )}
    </Drawer>
  );
};

export default ApprovalDetailDrawer;
