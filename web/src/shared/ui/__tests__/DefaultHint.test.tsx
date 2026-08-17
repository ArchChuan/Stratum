import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { DefaultHint } from '../DefaultHint';

describe('DefaultHint', () => {
  it('renders "默认：X" for a default value', () => {
    render(<DefaultHint value={0.7} />);
    expect(screen.getByText('默认：0.7')).toBeInTheDocument();
  });

  it('renders nothing when there is no default to show', () => {
    render(<DefaultHint value={null} />);
    expect(screen.queryByText(/默认/)).not.toBeInTheDocument();
  });

  it('supports custom label and suffix', () => {
    render(<DefaultHint value={0} suffix="（未设置）" label="平台兜底" />);
    expect(screen.getByText('平台兜底：0（未设置）')).toBeInTheDocument();
  });

  it('formats boolean defaults as 开/关', () => {
    render(<DefaultHint value={true} />);
    expect(screen.getByText('默认：开')).toBeInTheDocument();
  });
});
