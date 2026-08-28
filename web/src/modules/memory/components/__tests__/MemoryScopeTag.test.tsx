import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { MemoryScopeTag } from '../MemoryScopeTag';

describe('MemoryScopeTag', () => {
  it('renders user scope as 用户级', () => {
    render(<MemoryScopeTag scope="user" />);
    expect(screen.getByText('用户级')).toBeTruthy();
  });

  it('renders agent scope as Agent 级', () => {
    render(<MemoryScopeTag scope="agent" />);
    expect(screen.getByText('Agent 级')).toBeTruthy();
  });

  it('passes through an unknown scope so dirty data stays visible', () => {
    render(<MemoryScopeTag scope="legacy" />);
    expect(screen.getByText('legacy')).toBeTruthy();
  });

  it('renders a dash when scope is missing', () => {
    render(<MemoryScopeTag />);
    expect(screen.getByText('-')).toBeTruthy();
  });
});
