import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { RequestEditorButton } from '../RequestEditorButton';

import { operationProposalApi } from '@/modules/operation-gate';

vi.mock('antd', async (importOriginal) => {
  const mod = await importOriginal<typeof import('antd')>();
  return { ...mod, message: { success: vi.fn(), error: vi.fn() } };
});
vi.mock('@/modules/operation-gate', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/modules/operation-gate')>();
  return { ...mod, operationProposalApi: { requestEditorAccess: vi.fn().mockResolvedValue({}) } };
});

describe('RequestEditorButton', () => {
  it('knowledge_doc 渲染「申请查看权限」，其余渲染「申请编辑权限」', () => {
    render(<RequestEditorButton resourceType="knowledge_doc" resourceId="d1" />);
    expect(screen.getByRole('button', { name: '申请查看权限' })).toBeTruthy();
    render(<RequestEditorButton resourceType="workflow" resourceId="w1" />);
    expect(screen.getByRole('button', { name: '申请编辑权限' })).toBeTruthy();
  });

  it('点击触发申请', async () => {
    render(<RequestEditorButton resourceType="workflow" resourceId="w1" />);
    fireEvent.click(screen.getByRole('button', { name: '申请编辑权限' }));
    expect(await screen.findByRole('button', { name: '申请编辑权限' })).toBeTruthy();
    expect(operationProposalApi.requestEditorAccess).toHaveBeenCalled();
  });
});
