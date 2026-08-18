import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { queryResultSchema } from '../../model/knowledge';
import { WorkspaceQueryResult } from '../WorkspaceQueryResult';

// no_answer 存在 → 拒答提示替换绿色回答卡；null/undefined（旧后端或正常
// 回答）走原回答卡。nullish 判定兼容滚动升级。
describe('WorkspaceQueryResult refusal branch', () => {
  it('renders refusal alert with stats instead of the answer card when no_answer present', () => {
    const result = queryResultSchema.parse({
      answer: '',
      sources: [],
      no_answer: {
        reason: 'threshold_filtered',
        retrieved_count: 12,
        filtered_count: 10,
        best_score: 0.42,
        retried: false,
        rewritten_query: '',
        detail: '',
      },
      best_score: 0.42,
      candidate_count: 12,
    });
    render(<WorkspaceQueryResult result={result} />);

    // getByText 默认精确匹配整节点文本（message 是完整句子）→ 正则子串匹配
    expect(screen.getByText(/检索到的候选均未达到相关性阈值/)).toBeInTheDocument();
    // 统计拼接：候选数/过滤数/最高相关度
    expect(screen.getByText(/检索到 12 条候选/)).toBeInTheDocument();
    expect(screen.getByText(/阈值过滤 10 条/)).toBeInTheDocument();
    expect(screen.getByText(/最高相关度 42\.0%/)).toBeInTheDocument();
    // 拒答时不得渲染绿色回答卡
    expect(screen.queryByText('回答')).not.toBeInTheDocument();
  });

  it('renders retry note when retried with rewritten query', () => {
    const result = queryResultSchema.parse({
      answer: '',
      sources: [],
      no_answer: {
        reason: 'threshold_filtered',
        retrieved_count: 12,
        filtered_count: 12,
        best_score: 0.2,
        retried: true,
        rewritten_query: '改写后的查询',
        detail: '',
      },
    });
    render(<WorkspaceQueryResult result={result} />);
    expect(screen.getByText(/已改写查询重试：改写后的查询/)).toBeInTheDocument();
  });

  it('renders answer card when no_answer key is absent (old backend)', () => {
    const result = queryResultSchema.parse({
      answer: '这是一个正常回答',
      sources: [],
    });
    render(<WorkspaceQueryResult result={result} />);

    expect(screen.getByText('回答')).toBeInTheDocument();
    expect(screen.getByText('这是一个正常回答')).toBeInTheDocument();
  });

  it('renders answer card when no_answer is null', () => {
    const result = queryResultSchema.parse({
      answer: '正常回答（无答案信号为 null）',
      sources: [],
      no_answer: null,
    });
    render(<WorkspaceQueryResult result={result} />);

    expect(screen.getByText('回答')).toBeInTheDocument();
    expect(screen.getByText('正常回答（无答案信号为 null）')).toBeInTheDocument();
  });

  it('still renders source list under the answer card when sources exist', () => {
    const result = queryResultSchema.parse({
      answer: '带来源的回答',
      sources: [{ document_id: 'doc-1', score: 0.9, content: '来源片段', document_title: '文档A' }],
    });
    render(<WorkspaceQueryResult result={result} />);

    expect(screen.getByText('来源文档（1）')).toBeInTheDocument();
    expect(screen.getByText('文档: 文档A')).toBeInTheDocument();
  });
});
