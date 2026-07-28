import { render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it } from 'vitest';

import { AgentFormSections } from './AgentFormSections';

describe('AgentFormSections', () => {
  it('limits max iterations to the product range with a slider', () => {
    render(<Form initialValues={{ maxIterations: 10 }}><AgentFormSections skills={[]} mcpTools={[]} workspaces={[]} /></Form>);
    const slider = screen.getByRole('slider', { name: '最大迭代次数' });
    expect(slider).toHaveAttribute('aria-valuemin', '1');
    expect(slider).toHaveAttribute('aria-valuemax', '20');
    expect(slider).toHaveAttribute('aria-valuenow', '10');
  });
});
