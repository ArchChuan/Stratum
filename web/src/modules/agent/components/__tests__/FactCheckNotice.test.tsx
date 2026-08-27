import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { FactCheckReport } from '../../model/agent';
import FactCheckNotice from '../FactCheckNotice';

// FactCheckNotice 三态渲染：风险对账（warning/info Alert + 条目清单）、未验证
// 计数（info Alert）、全 verified 或 nullish 不渲染（advisory 只展示、防噪音）。
describe('FactCheckNotice risk rendering', () => {
  it('renders warning alert and lists risky tool references', () => {
    const factCheck: FactCheckReport = {
      checked: true,
      claims: [],
      isValid: false,
      riskPoints: 5,
      toolReferences: [
        { toolName: 'delete_order', claimText: '订单已删除', classification: 'verification_failed', risk: 5 },
        { toolName: 'send_mail', classification: 'outcome_unknown', risk: 2 },
      ],
      unverifiedCount: 0,
      unverifiedClaims: [],
    };
    render(<FactCheckNotice factCheck={factCheck} />);
    expect(screen.getByText(/回答存在可能的事实性风险（2 处工具调用核验未通过）/)).toBeInTheDocument();
    expect(screen.getByText(/工具调用核验失败/)).toBeInTheDocument();
    expect(screen.getByText(/delete_order：订单已删除/)).toBeInTheDocument();
    expect(screen.getByText(/工具调用结果未知/)).toBeInTheDocument();
  });

  // 机器引用标记 <tool_ref:ID> / [tool:ID] 是对账器的内部协议，非用户可见内容；
  // 对账报告逐条透出 claimText 时也必须剥离（与 ChatMarkdown 显示层策略一致）。
  it('strips tool_ref markers from claimText before display', () => {
    const factCheck: FactCheckReport = {
      checked: true,
      claims: [],
      isValid: false,
      riskPoints: 4,
      toolReferences: [
        {
          claimText: '《Agent 使用指南》<tool_ref:stratum_search_official_docs>',
          classification: 'invalid_reference',
          risk: 4,
        },
        { toolName: 'list_orders', claimText: '已列出订单 [tool:toolu_abc123]', classification: 'verification_failed', risk: 5 },
      ],
      unverifiedCount: 0,
      unverifiedClaims: [],
    };
    render(<FactCheckNotice factCheck={factCheck} />);
    expect(screen.getByText(/《Agent 使用指南》/)).toBeInTheDocument();
    expect(screen.getByText(/已列出订单/)).toBeInTheDocument();
    expect(screen.queryByText(/<tool_ref:/)).toBeNull();
    expect(screen.queryByText(/\[tool:/)).toBeNull();
    expect(screen.queryByText(/tool_ref:stratum_search_official_docs/)).toBeNull();
  });

  it('renders unverified count as info alert', () => {
    const factCheck: FactCheckReport = {
      checked: true,
      claims: [],
      isValid: true,
      riskPoints: 1,
      unverifiedCount: 2,
      unverifiedClaims: ['已删除订单', '已发送通知'],
    };
    render(<FactCheckNotice factCheck={factCheck} />);
    expect(
      screen.getByText(/有 2 条关于操作执行的陈述未附带工具调用引用（未验证）/),
    ).toBeInTheDocument();
  });

  it('renders nothing when all references are verified', () => {
    const factCheck: FactCheckReport = {
      checked: true,
      claims: [],
      isValid: true,
      riskPoints: 0,
      toolReferences: [{ toolName: 'delete_order', classification: 'verified', risk: 0 }],
      unverifiedCount: 0,
      unverifiedClaims: [],
    };
    const { container } = render(<FactCheckNotice factCheck={factCheck} />);
    expect(container.querySelector('.ant-alert')).toBeFalsy();
  });
});

describe('FactCheckNotice nullish compatibility', () => {
  // 校验关/旧后端无 factCheck 信号（nil/undefined）：不渲染，气泡保持原样
  it.each([undefined, null])('renders nothing when factCheck is %s', (factCheck) => {
    const { container } = render(<FactCheckNotice factCheck={factCheck} />);
    expect(container.querySelector('.ant-alert')).toBeFalsy();
  });
});
