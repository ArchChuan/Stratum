import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { vi } from 'vitest';

import { SkillWorkspacePage } from './SkillWorkspacePage';

const testDraft = vi.fn();

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
    testDraft: (...args: unknown[]) => testDraft(...args),
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
  beforeEach(() => {
    testDraft.mockReset();
  });

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

  it('运行草稿测试后展示输出、Trace 和耗时', async () => {
    testDraft.mockResolvedValue({
      result: { category: '物流' },
      traceID: 'trace-1',
      durationMs: 42,
    });
    renderWorkspace();

    fireEvent.click(await screen.findByRole('tab', { name: '测试' }));
    fireEvent.click(screen.getByRole('button', { name: /运行草稿测试/ }));

    expect(await screen.findByText('执行结果')).toBeInTheDocument();
    expect(screen.getByText(/"category": "物流"/)).toBeInTheDocument();
    expect(screen.getByText(/Trace：trace-1/)).toBeInTheDocument();
    expect(screen.getByText(/耗时：42 ms/)).toBeInTheDocument();
  });

  it('草稿执行成功但没有输出时展示明确说明', async () => {
    testDraft.mockResolvedValue({ result: '', traceID: 'trace-empty', durationMs: 8 });
    renderWorkspace();

    fireEvent.click(await screen.findByRole('tab', { name: '测试' }));
    fireEvent.click(screen.getByRole('button', { name: /运行草稿测试/ }));

    expect(await screen.findByText('执行成功，但 Skill 未返回内容')).toBeInTheDocument();
  });

  it('草稿执行失败时在测试页内展示错误说明', async () => {
    testDraft.mockRejectedValue(new Error('模型暂不可用'));
    renderWorkspace();

    fireEvent.click(await screen.findByRole('tab', { name: '测试' }));
    fireEvent.click(screen.getByRole('button', { name: /运行草稿测试/ }));

    expect(await screen.findByText('草稿测试失败：模型暂不可用')).toBeInTheDocument();
  });
});

const renderWorkspace = () => render(
  <MemoryRouter initialEntries={['/skills/skill-1/workspace']}>
    <Routes>
      <Route path="/skills/:id/workspace" element={<SkillWorkspacePage />} />
    </Routes>
  </MemoryRouter>,
);
