import { Alert } from 'antd';
import { memo } from 'react';

import type { NoAnswerInfo, NoAnswerReason } from '../model/agent';

// 拒答提示文案 map：reason 固定枚举（后端 pkg/constants 单一事实源），
// 文案只允许固定模板，禁止拼接检索内容等可变数据。
const REASON_TEXT: Record<NoAnswerReason, string> = {
  no_sources: '知识库中未检索到相关内容，未基于知识库作答。',
  threshold_filtered: '检索到的候选均未达到相关性阈值，未基于知识库作答。',
  access_restricted: '当前身份在知识库中无可见文档，无法基于知识库作答。',
  insufficient_evidence: '检索到的证据不足以支撑回答，未基于知识库作答。',
  unsupported_mode: '当前检索模式不被支持，未基于知识库作答。',
};

const REASON_DESCRIPTION: Partial<Record<NoAnswerReason, string>> = {
  threshold_filtered: '可尝试降低相关性阈值，或优化提问方式后重试。',
  no_sources: '可向知识库导入更多资料后再试。',
  access_restricted: '如需访问该知识库，请联系管理员授予权限。',
};

// NoAnswerNotice 渲染 RAG 无答案的拒答提示。reason 为 null/undefined 或未知
// 值时（旧后端无信号）不渲染——nullish 判定兼容滚动升级。
const NoAnswerNotice = memo(function NoAnswerNotice({ noAnswer }: { noAnswer?: NoAnswerInfo | null }) {
  if (!noAnswer) return null;
  const text = REASON_TEXT[noAnswer.Reason] ?? REASON_TEXT.no_sources;
  const description = REASON_DESCRIPTION[noAnswer.Reason];
  const detail =
    noAnswer.Detail && noAnswer.Detail !== text ? ` ${noAnswer.Detail}` : '';
  return (
    <Alert
      type="info"
      showIcon
      message={text}
      description={description ? `${description}${detail}` : detail || undefined}
      style={{ marginTop: 6 }}
    />
  );
});

export default NoAnswerNotice;
