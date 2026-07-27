import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi } from 'vitest';

import { SkillWorkspacePage } from './SkillWorkspacePage';

const { skillApiMock } = vi.hoisted(() => ({
  skillApiMock: {
    getWorkspace: vi.fn(), publish: vi.fn(), updateCapability: vi.fn(),
    updateActivation: vi.fn(), updateInstructions: vi.fn(),
  },
}));

const draftWorkspace = {
      skill: { id: 'skill-1', name: '测试 Skill', status: 'draft', draftRevisionId: 'revision-1' },
      draft: {
        id: 'revision-1', skillId: 'skill-1', status: 'draft',
        capability: { goal: '完成测试', whenToUse: '需要测试时' },
        activationContract: {
          name: 'test_skill', description: '用于测试', confirmed: false,
          inputSchema: { type: 'object' }, outputSchema: { type: 'object' },
        },
        instructions: '按照步骤完成测试',
        requirements: { mcpToolIds: ['mcp:test:read'], knowledgeWorkspaceIds: [], memoryScopes: ['conversation'] },
      },
};

vi.mock('../api/skill.api', () => ({ skillApi: skillApiMock }));
vi.mock('@/modules/iam', () => ({ useTenantRole: () => ({ isAdmin: true }) }));
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

it('展示 instruction Skill revision 编辑面', async () => {
  renderWorkspace();
  expect(await screen.findByRole('tab', { name: '激活契约' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '指令与权限' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Revision' })).toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: '实现' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /运行草稿测试/ })).not.toBeInTheDocument();
});

it('激活契约未确认时阻止发布并提供修复入口', async () => {
  renderWorkspace();
  fireEvent.click(await screen.findByRole('tab', { name: 'Revision' }));

  expect(screen.getByRole('button', { name: /发布当前 Revision/ })).toBeDisabled();
  expect(screen.getByText('发布前需要确认激活契约。')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '去确认激活契约' }));

  expect(screen.getByRole('tab', { name: '激活契约' })).toHaveAttribute('aria-selected', 'true');
  expect(skillApiMock.publish).not.toHaveBeenCalled();
});

it('发布成功后重新加载 workspace 并隐藏所有写操作', async () => {
  const confirmed = {
    ...draftWorkspace,
    draft: { ...draftWorkspace.draft, activationContract: { ...draftWorkspace.draft.activationContract, confirmed: true } },
  };
  const published = {
    skill: { id: 'skill-1', name: '测试 Skill', status: 'published', activeRevisionId: 'revision-1' },
    draft: { ...confirmed.draft, status: 'published', revisionNo: 1 },
  };
  skillApiMock.getWorkspace.mockResolvedValueOnce(confirmed).mockResolvedValueOnce(published);
  skillApiMock.publish.mockResolvedValue(published.draft);
  renderWorkspace();
  fireEvent.click(await screen.findByRole('tab', { name: 'Revision' }));
  fireEvent.click(screen.getByRole('button', { name: /发布当前 Revision/ }));

  await waitFor(() => expect(skillApiMock.getWorkspace).toHaveBeenCalledTimes(2));
  expect(await screen.findByText(/状态：published/)).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /发布当前 Revision/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab', { name: '能力' }));
  expect(screen.queryByRole('button', { name: '保存能力' })).not.toBeInTheDocument();
});

it('发布成功但刷新失败时不再暴露旧草稿操作', async () => {
  const confirmed = {
    ...draftWorkspace,
    draft: { ...draftWorkspace.draft, activationContract: { ...draftWorkspace.draft.activationContract, confirmed: true } },
  };
  skillApiMock.getWorkspace.mockResolvedValueOnce(confirmed).mockRejectedValueOnce(new Error('refresh failed'));
  skillApiMock.publish.mockResolvedValue({ ...confirmed.draft, status: 'published' });
  renderWorkspace();
  fireEvent.click(await screen.findByRole('tab', { name: 'Revision' }));
  fireEvent.click(screen.getByRole('button', { name: /发布当前 Revision/ }));

  expect(await screen.findByText('Revision 已发布，但工作台状态刷新失败。请重新进入页面确认最新状态。')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /发布当前 Revision/ })).not.toBeInTheDocument();
});
