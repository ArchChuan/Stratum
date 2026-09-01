import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { SuiteSummary } from '../model/evaluation';

import { SuitesPanel } from './SuitesPanel';

const suites: SuiteSummary[] = [
  { id: 's1', name: '投诉分类基线', description: '技能检索基线', status: 'draft', created_at: '2026-07-23T00:00:00Z' },
  { id: 's2', name: '已发布基线', description: '', status: 'published', created_at: '2026-07-24T00:00:00Z' },
];

describe('SuitesPanel', () => {
  it('lets an admin manage draft suites and create new ones', () => {
    const onOpen = vi.fn();
    render(<SuitesPanel suites={suites} loading={false} canManage onOpen={onOpen} onCreate={vi.fn()}
      onDelete={vi.fn()} canDelete={() => false} />);

    fireEvent.click(screen.getByRole('button', { name: '管理' }));
    expect(onOpen).toHaveBeenCalledWith(suites[0]);
    expect(screen.getByRole('button', { name: /新建套件/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '已发布' })).toBeDisabled();
  });

  it('keeps suite authoring read-only for members', () => {
    render(<SuitesPanel suites={suites} loading={false} canManage={false} onOpen={vi.fn()} onCreate={vi.fn()}
      onDelete={vi.fn()} canDelete={() => false} />);
    expect(screen.queryByRole('button', { name: /新建套件/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '管理' })).not.toBeInTheDocument();
  });

  it('shows delete only for rows the caller authorizes and fires onDelete', () => {
    const onDelete = vi.fn();
    render(<SuitesPanel suites={suites} loading={false} canManage={false} onOpen={vi.fn()} onCreate={vi.fn()}
      onDelete={onDelete} canDelete={(suite) => suite.id === 's1'} />);

    // 仅 s1 授权删除，s2 不显示删除按钮（type="link" 按钮不插入空格，文本为「删除」）
    const deleteButtons = screen.getAllByRole('button', { name: '删除' });
    expect(deleteButtons).toHaveLength(1);
    fireEvent.click(deleteButtons[0]);
    expect(onDelete).toHaveBeenCalledWith(suites[0]);
  });
});
