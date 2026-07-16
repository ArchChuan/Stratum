import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { WorkspaceDocumentsTable } from '../WorkspaceDocumentsTable';

let mobile = true;

vi.mock('@/shared/hooks', () => ({
  useResponsive: () => ({ isMobile: mobile }),
}));

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
  created_at: '2026-07-13T01:02:00Z',
  ingest_started_at: '2026-07-14T02:03:00Z',
};

describe('WorkspaceDocumentsTable', () => {
  beforeEach(() => {
    mobile = true;
  });

  it('shows document identity, status, progress and time on mobile', () => {
    render(<WorkspaceDocumentsTable documents={[documentRow]} loading={false} />);

    expect(screen.getByText('产品手册.pdf')).toBeInTheDocument();
    expect(screen.getByText('处理中')).toBeInTheDocument();
    expect(screen.getByText('3/10')).toBeInTheDocument();
    expect(screen.getByText(new Date(documentRow.created_at).toLocaleString('zh-CN'))).toBeInTheDocument();
    expect(document.querySelector('.ant-table')).not.toBeInTheDocument();
  });

  it('keeps the desktop table', () => {
    mobile = false;
    render(<WorkspaceDocumentsTable documents={[documentRow]} loading={false} />);

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
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: '删除文档' }));
    fireEvent.click(screen.getByRole('button', { name: /^删\s*除$/ }));
    expect(onDelete).toHaveBeenCalledWith('doc-1');
  });
});
