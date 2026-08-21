import { render, screen } from '@testing-library/react';
import { Form } from 'antd';
import { describe, expect, it } from 'vitest';

import { AgentMemoryConfig } from '../AgentMemoryConfig';

describe('AgentMemoryConfig', () => {
  it('renders resource settings as platform-default hints', () => {
    render(
      <Form>
        <AgentMemoryConfig groupedModels={[]} />
      </Form>,
    );
    expect(screen.getAllByText(/0 = 使用平台默认（资源未配置时生效）$/)).toHaveLength(3);
    expect(screen.getByText(/空 = 使用平台默认（资源未配置时生效）$/)).toBeInTheDocument();
  });

  it('keeps the 0 = 不注入历史 semantic for history injection', () => {
    render(
      <Form>
        <AgentMemoryConfig groupedModels={[]} />
      </Form>,
    );
    expect(screen.getByText(/0 = 不注入历史/)).toBeInTheDocument();
  });

  it('marks extraction prompt as required full system prompt with placeholders', () => {
    render(
      <Form>
        <AgentMemoryConfig groupedModels={[]} />
      </Form>,
    );
    // S2：完整系统提示词 + 占位符，未配置即失败；不再提供默认模板 viewer。
    expect(screen.getByText(/必填：完整系统提示词/)).toBeInTheDocument();
    expect(screen.getByText(/\{user_id\}/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '查看默认提示词' })).not.toBeInTheDocument();
  });
});
