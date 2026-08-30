import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkflowEditorsPanel } from '../WorkflowEditorsPanel';

vi.mock('@/modules/iam', () => ({
  useEditorCandidates: () => ({
    candidates: [
      { user_id: 'm-1', github_login: 'alice', role: 'member', joined_at: '2026-01-01' },
      { user_id: 'm-2', github_login: 'bob', role: 'member', joined_at: '2026-01-01' },
    ],
    loading: false,
  }),
}));

describe('WorkflowEditorsPanel', () => {
  const onSave = vi.fn().mockResolvedValue(undefined);

  beforeEach(() => onSave.mockClear());

  it('渲染当前白名单并在保存时提交原集合', async () => {
    render(<WorkflowEditorsPanel editors={['m-1']} onSave={onSave} />);
    expect(screen.getByRole('combobox')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(['m-1']));
  });

  it('新增成员后保存新集合', async () => {
    render(<WorkflowEditorsPanel editors={['m-1']} onSave={onSave} />);
    // antd Select 多选：打开下拉并点选选项（按 title 命中 github_login）。
    fireEvent.mouseDown(screen.getByRole('combobox'));
    fireEvent.click(await screen.findByTitle('bob'));
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(['m-1', 'm-2']));
  });
});
