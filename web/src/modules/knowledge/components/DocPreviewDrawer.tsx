import { Drawer, Empty, Skeleton, Typography, message } from 'antd';
import { useEffect, useState } from 'react';

import { knowledgeApi } from '../api/knowledge.api';
import type { ChunkSegment, DocumentPreview } from '../model/knowledge';

import { extractErrorMessage } from '@/shared/lib';

const { Text, Paragraph } = Typography;

// DocPreviewDrawer 展示文档原文预览（分块重组）。chat 来源卡片与文档表格共用；
// 由 name/documentID 自拉取，调用方只负责打开/关闭。
interface DocPreviewDrawerProps {
  open: boolean;
  name: string; // workspace name
  documentID: string;
  documentTitle?: string;
  onClose: () => void;
}

const renderSegment = (seg: ChunkSegment, index: number) => (
  <div key={seg.chunk_id || index} style={{ marginBottom: 12 }}>
    {seg.parent_content && (
      <Paragraph
        type="secondary"
        style={{
          margin: '0 0 6px',
          padding: '8px 10px',
          background: '#fafafa',
          borderLeft: '3px solid #d9d9d9',
          fontSize: 12,
          lineHeight: 1.6,
        }}
      >
        {seg.parent_content}
      </Paragraph>
    )}
    <Paragraph style={{ margin: 0, lineHeight: 1.7, whiteSpace: 'pre-wrap' }}>
      {seg.content || '（空片段）'}
    </Paragraph>
  </div>
);

export const DocPreviewDrawer = ({
  open,
  name,
  documentID,
  documentTitle,
  onClose,
}: DocPreviewDrawerProps) => {
  const [preview, setPreview] = useState<DocumentPreview | null>(null);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!open || !documentID) return;
    let cancelled = false;
    setLoading(true);
    setFailed(false);
    knowledgeApi
      .previewDocument(name, documentID)
      .then((data) => {
        if (!cancelled) setPreview(data);
      })
      .catch((err) => {
        if (cancelled) return;
        setFailed(true);
        message.error({ content: extractErrorMessage(err) || '加载预览失败', duration: 0 });
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, name, documentID]);

  return (
    <Drawer
      title={preview?.document_title || documentTitle || '文档预览'}
      open={open}
      onClose={onClose}
      width="min(640px, 92vw)"
    >
      {loading ? (
        <Skeleton active paragraph={{ rows: 8 }} />
      ) : failed ? (
        <Empty description="预览加载失败，可能已被删除或无访问权限" />
      ) : (
        <>
          <Text type="secondary" style={{ display: 'block', marginBottom: 16, fontSize: 12 }}>
            共 {preview?.chunk_count ?? 0} 个分块，内容由分块重组
          </Text>
          {(preview?.segments ?? []).map(renderSegment)}
        </>
      )}
    </Drawer>
  );
};

export default DocPreviewDrawer;
