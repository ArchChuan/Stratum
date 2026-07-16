import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi } from 'vitest';

import { SkillWorkspacePage } from './SkillWorkspacePage';

vi.mock('../api/skill.api', () => ({
  skillApi: {
    getWorkspace: vi.fn().mockResolvedValue({
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
    }),
  },
}));
vi.mock('@/modules/iam', () => ({ useTenantRole: () => ({ isAdmin: true }) }));
Object.defineProperty(window, 'matchMedia', { writable: true, value: vi.fn(() => ({
  matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(),
})) });

it('展示 instruction Skill revision 编辑面', async () => {
  render(<MemoryRouter initialEntries={['/skills/skill-1/workspace']}><Routes>
    <Route path="/skills/:id/workspace" element={<SkillWorkspacePage />} />
  </Routes></MemoryRouter>);
  expect(await screen.findByRole('tab', { name: '激活契约' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '指令与权限' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Revision' })).toBeInTheDocument();
  expect(screen.queryByRole('tab', { name: '实现' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /运行草稿测试/ })).not.toBeInTheDocument();
});
