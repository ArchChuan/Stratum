import { render, screen } from '@testing-library/react';
import { MemoryRouter, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';

import { evaluationRoutes } from './routes';

vi.mock('@/modules/iam', () => ({ PrivateRoute: ({ children }: { children: React.ReactNode }) => children }));
vi.mock('./pages/EvaluationCenterPage', () => ({ EvaluationCenterPage: () => <div>评测中心路由</div> }));

describe('evaluation routes', () => {
  it('registers the private evaluations route', () => {
    render(
      <MemoryRouter initialEntries={['/evaluations']} future={{ v7_relativeSplatPath: true, v7_startTransition: true }}>
        <Routes>{evaluationRoutes}</Routes>
      </MemoryRouter>,
    );
    expect(screen.getByText('评测中心路由')).toBeInTheDocument();
  });
});
