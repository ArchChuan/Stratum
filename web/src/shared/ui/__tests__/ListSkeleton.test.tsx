import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { ListSkeleton } from '../ListSkeleton';

describe('ListSkeleton', () => {
  it('renders the requested number of skeletons', () => {
    const { container } = render(<ListSkeleton count={5} />);
    expect(container.querySelectorAll('.ant-skeleton')).toHaveLength(5);
  });

  it('renders without card wrapper when card=false', () => {
    const { container } = render(<ListSkeleton count={2} card={false} />);
    expect(container.querySelectorAll('.ant-card')).toHaveLength(0);
    expect(container.querySelectorAll('.ant-skeleton')).toHaveLength(2);
  });
});
