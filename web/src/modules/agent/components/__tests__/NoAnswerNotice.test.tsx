import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { NoAnswerInfo, NoAnswerReason } from '../../model/agent';
import NoAnswerNotice from '../NoAnswerNotice';

// reason 文案矩阵：5 个固定 reason（后端 pkg/constants 单一事实源）逐值断言
// 文案可读；文案只允许固定模板，禁止拼接检索内容等可变数据。
const REASON_EXPECT: Array<[NoAnswerReason, string]> = [
  ['no_sources', '知识库中未检索到相关内容'],
  ['threshold_filtered', '检索到的候选均未达到相关性阈值'],
  ['access_restricted', '当前身份在知识库中无可见文档'],
  ['insufficient_evidence', '检索到的证据不足以支撑回答'],
  ['unsupported_mode', '当前检索模式不被支持'],
];

describe('NoAnswerNotice reason text matrix', () => {
  it.each(REASON_EXPECT)('renders refusal text for %s', (reason, text) => {
    render(<NoAnswerNotice noAnswer={{ Reason: reason } as NoAnswerInfo} />);
    // getByText 默认精确匹配整节点文本，文案是完整句子 → 正则子串匹配
    expect(screen.getByText(new RegExp(text))).toBeInTheDocument();
  });

  it('appends backend detail to the description', () => {
    render(
      <NoAnswerNotice
        noAnswer={{ Reason: 'threshold_filtered', Detail: '12 条候选，10 条未达阈值' } as NoAnswerInfo}
      />,
    );
    expect(screen.getByText(/12 条候选，10 条未达阈值/)).toBeInTheDocument();
  });
});

describe('NoAnswerNotice nullish compatibility', () => {
  // 旧后端无 noAnswer 信号（nil/undefined）：不渲染，气泡保持原样
  it.each([undefined, null])('renders nothing when noAnswer is %s', (noAnswer) => {
    const { container } = render(<NoAnswerNotice noAnswer={noAnswer} />);
    expect(container.querySelector('.ant-alert')).toBeFalsy();
  });
});
