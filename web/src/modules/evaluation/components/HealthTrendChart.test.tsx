import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { HealthTrendChart } from './HealthTrendChart';
import type { HealthTrendPoint } from './HealthTrendChart';

const point = (id: string, day: number, passRate: number | null, passed: boolean): HealthTrendPoint => ({
  id,
  timeLabel: `08-${String(day).padStart(2, '0')}`,
  fullLabel: `2026-08-${String(day).padStart(2, '0')}T02:00:00Z`,
  passRate,
  passed,
});

describe('HealthTrendChart', () => {
  it('breaks the line at a null-denominator point but still renders every status marker', () => {
    const points = [point('a', 1, 0.5, true), point('b', 2, null, false), point('c', 3, 0.8, true)];
    const { container } = render(<HealthTrendChart points={points} />);

    expect(screen.getByRole('img', { name: '评测运行通过率趋势折线图' })).toBeInTheDocument();
    const line = container.querySelector('svg path[stroke="#1677ff"]');
    expect(line).not.toBeNull();
    // null 点两侧不连线：0.5 与 0.8 各成一段子路径，而非贯穿中间空点的一条线。
    const moveCommands = (line!.getAttribute('d') || '').match(/M/g);
    expect(moveCommands).toHaveLength(2);

    const svg = container.querySelector('svg');
    // 有效分母点用实心圆点（通过）或叉号（未通过）标记，null 点用中性方点标记，均不缺失。
    expect(svg!.querySelectorAll('circle').length).toBe(2);
    expect(svg!.querySelectorAll('rect').length).toBe(1);
  });

  it('draws one continuous line when every point has a valid denominator', () => {
    const points = [point('a', 1, 0.5, true), point('b', 2, 0.6, false), point('c', 3, 0.8, true)];
    const { container } = render(<HealthTrendChart points={points} />);

    const line = container.querySelector('svg path[stroke="#1677ff"]');
    expect((line!.getAttribute('d') || '').match(/M/g)).toHaveLength(1);
    // 未通过但分母有效仍以叉号 path 标记，而非落入 null 方点分支。
    const svg = container.querySelector('svg');
    expect(svg!.querySelectorAll('circle').length).toBe(2);
    expect(svg!.querySelectorAll('rect').length).toBe(0);
  });
});
