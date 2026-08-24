import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { DiagnosticReport } from '../DiagnosticReport';

describe('DiagnosticReport', () => {
  it('把事实、缺口、建议、工具耗时和引用分区展示', () => {
    const { container } = render(<DiagnosticReport report={{
      facts: [{
        area: 'agent', statement: 'Agent 可正常读取', source: 'agent_repository',
        observedAt: '2026-07-23T12:00:00Z',
      }],
      inferences: ['当前配置满足基础使用条件'],
      evidenceGaps: [{ area: 'mcp', source: 'mcp_repository', code: 'evidence_timeout' }],
      recommendedActions: ['检查 MCP Server 连通性'],
      steps: [{ tool: 'stratum_diagnose_tenant', outcome: 'partial', latencyMs: 23 }],
      citations: [{
        documentId: 'agent-guide', title: 'Agent 使用指南', productVersion: 'v1',
        section: '模型配置', url: 'https://docs.example.test/agent', excerpt: '先配置租户模型。',
      }],
    }} />);

    expect(screen.getByText('诊断证据')).toBeInTheDocument();
    fireEvent.click(screen.getByText('诊断证据'));
    expect(screen.getByText('已确认事实')).toBeInTheDocument();
    expect(screen.getByText('证据缺口')).toBeInTheDocument();
    expect(screen.getByText('建议操作')).toBeInTheDocument();
    expect(screen.getByText('工具步骤与耗时')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Agent 使用指南/ })).toHaveAttribute(
      'href', 'https://docs.example.test/agent',
    );
    expect(screen.getByText('23 ms')).toBeInTheDocument();
    expect(container.querySelector('.diagnostic-report')).toHaveStyle({ minWidth: 0 });
    expect(container.querySelector('.diagnostic-report-content')).toHaveStyle({ overflowWrap: 'anywhere' });
  });
});
