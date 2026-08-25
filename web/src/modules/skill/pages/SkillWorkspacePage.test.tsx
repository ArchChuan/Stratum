import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi } from 'vitest';

import { SkillWorkspacePage } from './SkillWorkspacePage';

const { skillApiMock } = vi.hoisted(() => ({
  skillApiMock: {
    getWorkspace: vi.fn(), updateSkill: vi.fn(), listRevisions: vi.fn(), rollback: vi.fn(),
  },
}));

const workspace = {
  skill: { id: 'skill-1', name: '测试 Skill', status: 'published', activeRevisionId: 'revision-1' },
  active: {
    id: 'revision-1', skillId: 'skill-1', status: 'published', revisionNo: 1,
    name: '测试 Skill', description: '用于测试', instructions: '按照步骤完成测试',
    contentHash: 'hash-v1',
  },
  editors: [],
};

vi.mock('../api/skill.api', () => ({ skillApi: skillApiMock }));
vi.mock('@/modules/iam', () => ({
  useTenantRole: () => ({ isAdmin: true }),
  useAuth: () => ({ user: { sub: 'user-1' } }),
  useEditorCandidates: () => ({ candidates: [], loading: false }),
}));
Object.defineProperty(window, 'matchMedia', { writable: true, value: vi.fn(() => ({
  matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(),
})) });

const renderWorkspace = () => render(<MemoryRouter initialEntries={['/skills/skill-1/workspace']}><Routes>
  <Route path="/skills/:id/workspace" element={<SkillWorkspacePage />} />
</Routes></MemoryRouter>);

beforeEach(() => {
  vi.clearAllMocks();
  skillApiMock.getWorkspace.mockResolvedValue(workspace);
});

it('展示版本化编辑面：指令/可编辑人/版本历史', async () => {
  renderWorkspace();
  expect(await screen.findByRole('tab', { name: '指令' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '可编辑人' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '版本历史' })).toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: 'Revision' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /发布当前 Revision/ })).not.toBeInTheDocument();
});

it('保存即生效：PATCH 携带当前生效版本基线，成功后头部版本号前进', async () => {
  skillApiMock.updateSkill.mockResolvedValue({
    ...workspace,
    active: {
      ...workspace.active, id: 'revision-2', revisionNo: 2, instructions: '更新后的步骤', contentHash: 'hash-v2',
    },
  });
  renderWorkspace();
  const instructions = await screen.findByLabelText('执行指令');
  fireEvent.change(instructions, { target: { value: '更新后的步骤' } });
  fireEvent.click(screen.getByRole('button', { name: /保存并立即生效/ }));

  await waitFor(() => expect(skillApiMock.updateSkill).toHaveBeenCalledWith('skill-1', {
    name: '测试 Skill', description: '用于测试', instructions: '更新后的步骤',
    expectedContentHash: 'hash-v1',
  }));
  expect(await screen.findByText(/当前版本：v2/)).toBeInTheDocument();
});

it('版本历史列出当前生效与历史版本，回滚历史版本需确认', async () => {
  skillApiMock.listRevisions.mockResolvedValue([
    { ...workspace.active, isCurrent: true, createdAt: '2026-02-01T00:00:00Z' },
    {
      ...workspace.active, id: 'revision-0', status: 'deprecated', revisionNo: 1,
      isCurrent: false, createdAt: '2026-01-01T00:00:00Z',
    },
  ]);
  renderWorkspace();
  fireEvent.click(await screen.findByRole('tab', { name: '版本历史' }));

  expect(await screen.findByText('当前生效')).toBeInTheDocument();
  expect(screen.getByText('历史')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '回滚' }));
  // antd Modal.confirm 同时渲染 ant-modal-title 与 ant-modal-confirm-title 两份标题。
  expect((await screen.findAllByText('回滚到版本 v1？')).length).toBeGreaterThan(0);

  // antd 中文双字按钮在字符间加字距空格（modal 确认按钮 name 为「回 滚」），用正则匹配。
  const confirmButtons = screen.getAllByRole('button', { name: /回\s*滚/ });
  fireEvent.click(confirmButtons[confirmButtons.length - 1]);
  await waitFor(() => expect(skillApiMock.rollback).toHaveBeenCalledWith('skill-1', 'revision-0'));
});
