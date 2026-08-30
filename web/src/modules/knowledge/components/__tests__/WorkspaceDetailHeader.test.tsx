import { render, screen } from '@testing-library/react';
import { createElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkspaceDetailHeader } from '../WorkspaceDetailHeader';

// 可控 stub：断言 workspace 申请入口的插槽逻辑（props 透传），点击/请求行为由
// RequestEditorButton 自身测试覆盖，此处不重复。
const { requestEditorButtonMock } = vi.hoisted(() => ({ requestEditorButtonMock: vi.fn() }));

vi.mock('@/shared/components', () => ({ RequestEditorButton: requestEditorButtonMock }));

beforeEach(() => {
  requestEditorButtonMock.mockReset();
  requestEditorButtonMock.mockImplementation(
    (props: {
      resourceType: string;
      resourceId: string;
      options?: { resourceName?: string };
    }) =>
      createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'request-editor-button',
          'data-resource-type': props.resourceType,
          'data-resource-id': props.resourceId,
        },
        props.resourceType === 'knowledge_workspace' ? '申请编辑权限' : '申请查看权限',
      ),
  );
});

describe('WorkspaceDetailHeader', () => {
  it('canRequestEditor 为 true 时渲染「申请编辑权限」入口并透传 workspace 参数', () => {
    render(<WorkspaceDetailHeader name="产品库" onBack={vi.fn()} canRequestEditor />);

    expect(screen.getByRole('button', { name: '申请编辑权限' })).toBeInTheDocument();
    // React 渲染函数组件时附带 legacy-context 第二参，仅断言首参 props。
    expect(requestEditorButtonMock.mock.calls[0][0]).toMatchObject({
      resourceType: 'knowledge_workspace',
      resourceId: '产品库',
      options: { resourceName: '产品库' },
    });
  });

  it('缺省不渲染申请入口', () => {
    render(<WorkspaceDetailHeader name="产品库" onBack={vi.fn()} />);

    expect(screen.queryByRole('button', { name: '申请编辑权限' })).not.toBeInTheDocument();
    expect(requestEditorButtonMock).not.toHaveBeenCalled();
  });

  it('admin 视图（canRequestEditor 缺省）不渲染申请入口', () => {
    render(<WorkspaceDetailHeader name="产品库" onBack={vi.fn()} />);

    expect(screen.queryByTestId('request-editor-button')).not.toBeInTheDocument();
  });
});
