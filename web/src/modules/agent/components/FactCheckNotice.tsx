import { Alert, Space, Tag } from 'antd';
import { memo } from 'react';

import type { FactCheckReport, ToolReferenceClassification } from '../model/agent';

// toolRefRe 与 ChatMarkdown/后端 factcheck/reconcile.go 的字符集一致：
// <tool_ref:ID> 与兼容形式 [tool:ID]，ID 为 tool_call_id 合法字符。该标记是给
// 模型/对账器的机器指令，非用户可见内容；对账报告逐条透出 claimText 时同样
// 必须先剥离（纯显示层，不改消息源）。
const TOOL_REF_RE = /<tool_ref:[A-Za-z0-9_-]+>|\[tool:[A-Za-z0-9_-]+\]/g;

const stripToolRef = (text?: string | null): string =>
  (text ?? '').replace(TOOL_REF_RE, '');

// 对账判定文案（与后端五态枚举对齐；verified 折叠不展示，避免噪音）。
const CLASSIFICATION_TEXT: Record<string, string> = {
  verification_failed: '工具调用核验失败',
  invalid_reference: '无效的工具调用引用',
  outcome_unknown: '工具调用结果未知',
  unverified: '未附带工具调用引用',
};

// 对账判定 Tag 颜色（advisory 展示，不硬判假话）。
const CLASSIFICATION_COLOR: Record<string, string> = {
  verification_failed: 'red',
  invalid_reference: 'orange',
  outcome_unknown: 'gold',
  unverified: 'blue',
};

// 需在报告中展开的对账分类：verified 折叠；unverified 单独走 unverifiedClaims
// 计数（后端不写入 toolReferences）。verification_failed / invalid_reference
// 已使整体 isValid=false，outcome_unknown 为 advisory。
const NOTICE_CLASSIFICATIONS: ToolReferenceClassification[] = [
  'verification_failed',
  'invalid_reference',
  'outcome_unknown',
];

// FactCheckNotice 渲染幻觉防护对账报告（advisory，只展示）。factCheck 为
// null/undefined（校验关/旧后端无信号）或全为 verified 时返回 null——nullish
// 判定兼容滚动升级，verified 折叠避免噪音。
const FactCheckNotice = memo(function FactCheckNotice({
  factCheck,
}: {
  factCheck?: FactCheckReport | null;
}) {
  if (!factCheck) return null;
  const risky = (factCheck.toolReferences ?? []).filter((r) =>
    NOTICE_CLASSIFICATIONS.includes(r.classification),
  );
  const showRisky = risky.length > 0;
  const unverifiedCount = factCheck.unverifiedCount ?? 0;
  if (!showRisky && unverifiedCount <= 0) return null;
  return (
    <div style={{ marginTop: 6 }}>
      {showRisky && (
        <Alert
          type={factCheck.isValid === false ? 'warning' : 'info'}
          showIcon
          message={`回答存在可能的事实性风险（${risky.length} 处工具调用核验未通过）`}
          description={
            <Space direction="vertical" size={2} style={{ width: '100%' }}>
              {risky.map((r, i) => (
                <div
                  key={`${r.toolCallId || r.reference || 'ref'}-${i}`}
                  style={{ fontSize: 12 }}
                >
                  <Tag color={CLASSIFICATION_COLOR[r.classification] || 'default'}>
                    {CLASSIFICATION_TEXT[r.classification] || r.classification}
                  </Tag>
                  <span>
                    {r.toolName ? `${stripToolRef(r.toolName)}：` : ''}
                    {stripToolRef(r.claimText) || '该操作声称未通过核验'}
                  </span>
                  {r.risk > 0 ? (
                    <span style={{ color: '#8c8c8c' }}>（风险 {r.risk}）</span>
                  ) : null}
                </div>
              ))}
            </Space>
          }
        />
      )}
      {unverifiedCount > 0 && (
        <Alert
          type="info"
          showIcon
          message={`有 ${unverifiedCount} 条关于操作执行的陈述未附带工具调用引用（未验证）`}
          style={showRisky ? { marginTop: 6 } : undefined}
        />
      )}
    </div>
  );
});

export default FactCheckNotice;
