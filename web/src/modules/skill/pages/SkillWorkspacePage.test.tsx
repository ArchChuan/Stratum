import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi } from 'vitest';

import { SkillWorkspacePage } from './SkillWorkspacePage';

vi.mock('../api/skill.api', () => ({
  skillApi: {
    getWorkspace: vi.fn().mockResolvedValue({
      skill: {
        id: 'skill-1', name: '测试 Skill', description: '', status: 'published',
        activeVersionId: 'version-1', draftVersionId: 'version-1',
      },
      draft: {
        id: 'version-1', skillId: 'skill-1', versionNo: 1, status: 'published',
        capability: {}, toolContract: {}, implementation: {},
      },
    }),
  },
}));

vi.mock('@/modules/iam', () => ({ useTenantRole: () => ({ isAdmin: true }) }));

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

describe('SkillWorkspacePage', () => {
  it('为已发布 Skill 展示评测与优化页签', async () => {
    render(
      <MemoryRouter initialEntries={['/skills/skill-1/workspace']}>
        <Routes>
          <Route path="/skills/:id/workspace" element={<SkillWorkspacePage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByRole('tab', { name: '评测与优化' })).toBeInTheDocument();
  });
});
