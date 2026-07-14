import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Form } from 'antd';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import { FormPage } from '../FormPage';
import { ResourceListPage } from '../ResourceListPage';
import { SectionHeader } from '../SectionHeader';

const FormPageHarness = ({ onFinish, onBack }: {
  onFinish: (values: { name: string }) => void;
  onBack: () => void;
}) => {
  const [form] = Form.useForm<{ name: string }>();
  return (
    <FormPage
      title="编辑资源"
      form={form}
      initialValues={{ name: '测试资源' }}
      onFinish={onFinish}
      onBack={onBack}
    >
      <Form.Item name="name"><input aria-label="名称" /></Form.Item>
    </FormPage>
  );
};

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: vi.fn((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
});

describe('responsive page primitives', () => {
  it('keeps resource list header actions responsive and callable', () => {
    const onCreate = vi.fn();
    const toolbarAction = vi.fn();
    const { container } = render(
      <ResourceListPage
        title="资源"
        loading={false}
        items={[{ id: '1' }]}
        renderItem={(item) => <div key={item.id}>{item.id}</div>}
        onCreate={onCreate}
        toolbarExtra={<button type="button" onClick={toolbarAction}>筛选</button>}
      />,
    );

    expect(container.querySelector('.responsive-page-header')).toBeInTheDocument();
    expect(container.querySelector('.responsive-toolbar')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '筛选' }));
    fireEvent.click(screen.getByRole('button', { name: /新建/ }));
    expect(toolbarAction).toHaveBeenCalledTimes(1);
    expect(onCreate).toHaveBeenCalledTimes(1);
  });

  it('keeps form submit and back behavior in the responsive action group', async () => {
    const onFinish = vi.fn();
    const onBack = vi.fn();
    const { container } = render(<FormPageHarness onFinish={onFinish} onBack={onBack} />);

    expect(container.querySelector('.responsive-form-actions')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /保\s*存/ }));
    await waitFor(() => expect(onFinish).toHaveBeenCalledWith({ name: '测试资源' }));
    fireEvent.click(screen.getByRole('button', { name: /返\s*回/ }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it('marks long section headings for safe responsive wrapping', () => {
    const { container } = render(
      <SectionHeader
        icon={<span>图</span>}
        title={'很长的标题'.repeat(20)}
        subtitle={'很长的说明'.repeat(20)}
      />,
    );

    expect(container.querySelector('.responsive-section-header')).toBeInTheDocument();
    const content = container.querySelector<HTMLDivElement>(
      '.responsive-section-header > div:last-child',
    );
    expect(content?.style.minWidth).toBe('0');
    expect(content?.style.overflowWrap).toBe('anywhere');
  });
});
