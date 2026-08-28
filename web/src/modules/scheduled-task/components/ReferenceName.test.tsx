import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { ReferenceName } from './ReferenceName';

describe('ReferenceName', () => {
  it('shows the readable name by default and keeps the raw id for hover', () => {
    render(<ReferenceName name="日报生成" id="wf-1" />);
    expect(screen.getByText('日报生成')).toBeInTheDocument();
    expect(screen.queryByText('wf-1')).not.toBeInTheDocument();
  });

  it('falls back to the raw id when no name is resolved', () => {
    render(<ReferenceName id="wf-1" />);
    expect(screen.getByText('wf-1')).toBeInTheDocument();
  });

  it('falls back to the raw id for an empty name', () => {
    render(<ReferenceName name="" id="ver-2" />);
    expect(screen.getByText('ver-2')).toBeInTheDocument();
  });
});
