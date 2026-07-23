import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import { buildMenuItems, resolveOpenKeys } from '../menu.config';

const collectLabels = (items: ReturnType<typeof buildMenuItems>): ReactNode[] =>
  items.flatMap((item) => {
    if (!item || typeof item !== 'object') return [];
    const current = 'label' in item ? [item.label] : [];
    const children = 'children' in item && Array.isArray(item.children)
      ? collectLabels(item.children)
      : [];
    return [...current, ...children];
  });

describe('buildMenuItems', () => {
  it('hides tenant management routes from members', () => {
    const labels = collectLabels(buildMenuItems({
          sub: 'user-1',
          tenant_id: 'tenant-1',
          role: 'member',
          avatar_url: '',
          github_login: 'member',
          current_tenant: { id: 'tenant-1', name: 'Test', role: 'member' },
        }));
    render(
      <MemoryRouter future={{ v7_relativeSplatPath: true, v7_startTransition: true }}>
        {labels.map((label, index) => <div key={index}>{label}</div>)}
      </MemoryRouter>,
    );

    expect(screen.getByRole('link', { name: 'Agent 列表' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '技能列表' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '服务器列表' })).toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '创建 Agent' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '创建技能' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '添加服务器' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '评测与进化' })).toHaveAttribute('href', '/evaluations');
  });

  it('opens the evaluation navigation group', () => {
    expect(resolveOpenKeys('/evaluations')).toEqual(['evaluation-group']);
  });

  it('does not expose execution history in navigation', () => {
    const items = buildMenuItems({
      sub: 'user-1',
      tenant_id: 'tenant-1',
      role: 'member',
      avatar_url: '',
      github_login: 'member',
      current_tenant: { id: 'tenant-1', name: 'Test', role: 'member' },
    });

    const labels = collectLabels(items);
    render(
      <MemoryRouter future={{ v7_relativeSplatPath: true, v7_startTransition: true }}>
        {labels.map((label, index) => <div key={index}>{label}</div>)}
      </MemoryRouter>,
    );

    expect(screen.queryByRole('link', { name: '执行历史' })).not.toBeInTheDocument();
    expect(items.some((item) => item && 'key' in item && item.key === '/history')).toBe(false);
    expect(resolveOpenKeys('/history')).toEqual([]);
    expect(resolveOpenKeys('/agents')).toEqual(['agent-group']);
  });
});
