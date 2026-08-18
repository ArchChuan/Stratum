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

  it('offers the extraction prompt viewer with rule-increment notes', () => {
    render(
      <Form>
        <AgentMemoryConfig groupedModels={[]} />
      </Form>,
    );
    // 方案 B：extraction_prompt 是规则增量，不提示任何 %s/%d 占位符。
    expect(screen.getByText(/仅作为附加规则/)).toBeInTheDocument();
    expect(screen.getByText(/无需填写占位符/)).toBeInTheDocument();
    expect(screen.queryByText(/%s\(userID\)/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '查看默认提示词' })).toBeInTheDocument();
  });
});
