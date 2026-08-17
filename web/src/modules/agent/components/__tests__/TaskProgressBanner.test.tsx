import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { TaskSnapshot } from '../../model/agent';
import TaskProgressBanner from '../TaskProgressBanner';

const snapshot: TaskSnapshot = {
  goal: '迁移订单服务到新架构',
  currentPhase: '1/2 完成',
  completedSteps: ['n1'],
  nextAction: '验证迁移',
  status: 'active',
};

describe('TaskProgressBanner', () => {
  it('renders goal, phase and next action', () => {
    render(<TaskProgressBanner snapshot={snapshot} />);
    expect(screen.getByText('迁移订单服务到新架构')).toBeTruthy();
    expect(screen.getByText(/1\/2 完成/)).toBeTruthy();
    expect(screen.getByText(/验证迁移/)).toBeTruthy();
  });

  it('renders completed state', () => {
    render(<TaskProgressBanner snapshot={{ ...snapshot, status: 'completed', nextAction: '' }} />);
    expect(screen.getByText(/已完成/)).toBeTruthy();
  });
});
