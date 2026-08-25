import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi } from 'vitest';

import { SkillWorkspacePage } from './SkillWorkspacePage';

const { skillApiMock } = vi.hoisted(() => ({
  skillApiMock: {
    getWorkspace: vi.fn(), publish: vi.fn(), updateDraft: vi.fn(),
  },
}));

const draftWorkspace = {
      skill: { id: 'skill-1', name: '测试 Skill', status: 'draft', draftRevisionId: 'revision-1' },
      draft: {
        id: 'revision-1', skillId: 'skill-1', status: 'draft',
        name: '测试 Skill', description: '用于测试', instructions: '按照步骤完成测试',
      },
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
  skillApiMock.getWorkspace.mockResolvedValue(draftWorkspace);
});

it('展示简化后的编辑面：指令/可编辑人/Revision', async () => {
  renderWorkspace();
  expect(await screen.findByRole('tab', { name: '指令' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '可编辑人' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Revision' })).toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: '能力' })).not.toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: '激活契约' })).not.toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: '评测与优化' })).not.toBeInTheDocument();
});

it('发布无需确认激活契约：Revision tab 不显示确认门槛', async () => {
  renderWorkspace();
  fireEvent.click(await screen.findByRole('tab', { name: 'Revision' }));

  expect(screen.queryByText('发布前需要确认激活契约。')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '去确认激活契约' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /发布当前 Revision/ })).toBeEnabled();
});

it('发布成功后重新加载 workspace 并隐藏所有写操作', async () => {
  const published = {
    skill: { id: 'skill-1', name: '测试 Skill', status: 'published', activeRevisionId: 'revision-1' },
    draft: { ...draftWorkspace.draft, status: 'published', revisionNo: 1 },
  };
  skillApiMock.getWorkspace.mockResolvedValueOnce(draftWorkspace).mockResolvedValueOnce(published);
  skillApiMock.publish.mockResolvedValue(published.draft);
  renderWorkspace();
  fireEvent.click(await screen.findByRole('tab', { name: 'Revision' }));
  fireEvent.click(screen.getByRole('button', { name: /发布当前 Revision/ }));

  await waitFor(() => expect(skillApiMock.getWorkspace).toHaveBeenCalledTimes(2));
  expect(await screen.findByText(/状态：published/)).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /发布当前 Revision/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: '指令' }));
  expect(screen.queryByRole('button', { name: '保存指令' })).not.toBeInTheDocument();
});

it('发布成功但刷新失败时不再暴露旧草稿操作', async () => {
  skillApiMock.getWorkspace.mockResolvedValueOnce(draftWorkspace).mockRejectedValueOnce(new Error('refresh failed'));
  skillApiMock.publish.mockResolvedValue({ ...draftWorkspace.draft, status: 'published' });
  renderWorkspace();
  fireEvent.click(await screen.findByRole('tab', { name: 'Revision' }));
  fireEvent.click(screen.getByRole('button', { name: /发布当前 Revision/ }));

  expect(await screen.findByText('Revision 已发布，但工作台状态刷新失败。请重新进入页面确认最新状态。')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /发布当前 Revision/ })).not.toBeInTheDocument();
});
