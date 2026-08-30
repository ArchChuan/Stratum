import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkspaceDocumentsTable } from '../WorkspaceDocumentsTable';

import { operationProposalApi } from '@/modules/operation-gate';

let mobile = true;

vi.mock('@/shared/hooks', () => ({
  useResponsive: () => ({ isMobile: mobile }),
}));

// RequestEditorButton 内部经 useRequestEditorAccess 调用 operationProposalApi；
// 此处沿用 RequestEditorButton.test 的 mock 策略，验证「申请查看权限」点击真实触发请求。
vi.mock('antd', async (importOriginal) => {
  const mod = await importOriginal<typeof import('antd')>();
  return { ...mod, message: { success: vi.fn(), error: vi.fn() } };
});
vi.mock('@/modules/operation-gate', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/modules/operation-gate')>();
  return { ...mod, operationProposalApi: { requestEditorAccess: vi.fn().mockResolvedValue({}) } };
});

beforeAll(() => {
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: false,
    addListener: vi.fn(),
    removeListener: vi.fn(),
  })));
});

const documentRow = {
  id: 'doc-1',
  source: '产品手册.pdf',
  content_hash: 'hash',
  ingest_status: 'processing',
  ingest_error: '',
  processed_chunks: 3,
  total_chunks: 10,
  allowed_user_ids: [],
  allowed_role_ids: [],
  restricted: false,
  created_at: '2026-07-13T01:02:00Z',
  ingest_started_at: '2026-07-14T02:03:00Z',
};

describe('WorkspaceDocumentsTable', () => {
  beforeEach(() => {
    mobile = true;
  });

  it('shows document identity, status, progress and time on mobile', () => {
    render(<WorkspaceDocumentsTable documents={[documentRow]} loading={false} workspaceName="产品库" />);

    expect(screen.getByText('产品手册.pdf')).toBeInTheDocument();
    expect(screen.getByText('处理中')).toBeInTheDocument();
    expect(screen.getByText('3/10')).toBeInTheDocument();
    expect(screen.getByText(new Date(documentRow.created_at).toLocaleString('zh-CN'))).toBeInTheDocument();
    expect(document.querySelector('.ant-table')).not.toBeInTheDocument();
  });

  it('keeps the desktop table', () => {
    mobile = false;
    render(<WorkspaceDocumentsTable documents={[documentRow]} loading={false} workspaceName="产品库" />);

    expect(document.querySelector('.ant-table')).toBeInTheDocument();
  });

  it('disables deletion while processing', () => {
    render(
      <WorkspaceDocumentsTable
        documents={[documentRow]}
        loading={false}
        isAdmin
        deletingDocumentID=""
        onDelete={vi.fn()}
        workspaceName="产品库"
      />,
    );

    expect(screen.getByRole('button', { name: '删除文档' })).toBeDisabled();
  });

  it('confirms deletion for a completed document', () => {
    const onDelete = vi.fn();
    render(
      <WorkspaceDocumentsTable
        documents={[{ ...documentRow, ingest_status: 'completed' }]}
        loading={false}
        isAdmin
        deletingDocumentID=""
        onDelete={onDelete}
        workspaceName="产品库"
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: '删除文档' }));
    fireEvent.click(screen.getByRole('button', { name: /^删\s*除$/ }));
    expect(onDelete).toHaveBeenCalledWith('doc-1');
  });

  it('member 受限文档显示「申请查看权限」，点击触发申请请求', async () => {
    const restricted = {
      ...documentRow,
      restricted: true,
      allowed_user_ids: ['u-1'],
      allowed_role_ids: [],
    };
    render(
      <WorkspaceDocumentsTable
        documents={[restricted]}
        loading={false}
        workspaceName="产品库"
      />,
    );

    const button = screen.getByRole('button', { name: '申请查看权限' });
    expect(button).toBeInTheDocument();
    fireEvent.click(button);
    expect(operationProposalApi.requestEditorAccess).toHaveBeenCalledWith(
      'knowledge_doc',
      'doc-1',
      { workspaceName: '产品库', resourceName: '产品库/产品手册.pdf' },
    );
  });

  it('member 非受限文档不显示「申请查看权限」', () => {
    render(<WorkspaceDocumentsTable documents={[documentRow]} loading={false} workspaceName="产品库" />);
    expect(screen.queryByRole('button', { name: '申请查看权限' })).not.toBeInTheDocument();
  });

  it('admin 不显示「申请查看权限」', () => {
    const restricted = {
      ...documentRow,
      restricted: true,
      allowed_user_ids: ['u-1'],
      allowed_role_ids: [],
    };
    render(
      <WorkspaceDocumentsTable
        documents={[restricted]}
        loading={false}
        isAdmin
        workspaceName="产品库"
        onDelete={vi.fn()}
        onSetAccess={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button', { name: '申请查看权限' })).not.toBeInTheDocument();
  });

  it('desktop 表格行内同样渲染「申请查看权限」', () => {
    mobile = false;
    const restricted = {
      ...documentRow,
      restricted: true,
      allowed_user_ids: ['u-1'],
      allowed_role_ids: [],
    };
    render(<WorkspaceDocumentsTable documents={[restricted]} loading={false} workspaceName="产品库" />);
    expect(screen.getByRole('button', { name: '申请查看权限' })).toBeInTheDocument();
  });
});
