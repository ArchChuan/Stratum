import { Badge, Divider, Space, Tag, Typography } from 'antd';
import { useState } from 'react';

import type { QueryResult, QuerySource } from '../model/knowledge';

import { DocPreviewDrawer } from './DocPreviewDrawer';

const { Text, Paragraph } = Typography;

interface WorkspaceQueryResultProps {
  result: QueryResult;
}

// 文档名可点击打开原文预览（P1.4）；workspace/document_title 由 P1.1 的
// query 响应携带，缺失（旧后端）时退化为不可点击的截断 id。
const SourceItem = ({ source }: { source: QuerySource }) => {
  const [previewOpen, setPreviewOpen] = useState(false);
  const title = source.document_title || source.document_id || '';
  const previewable = Boolean(source.workspace && source.document_id);

  return (
    <div
      className="long-text"
      style={{
        background: '#fafafa',
        border: '1px solid #f0f0f0',
        padding: '10px 14px',
      }}
    >
      <Space size={8} style={{ marginBottom: 6 }}>
        <Tag
          style={{ margin: 0, cursor: previewable ? 'pointer' : 'default' }}
          color={previewable ? 'blue' : undefined}
          onClick={previewable ? () => setPreviewOpen(true) : undefined}
        >
          文档: {title.slice(0, 32) || '-'}
        </Tag>
        <Badge
          count={`${((source.score ?? 0) * 100).toFixed(1)}%`}
          style={{ background: '#52c41a', fontSize: 11 }}
        />
      </Space>
      <Paragraph
        ellipsis={{ rows: 2 }}
        type="secondary"
        style={{ margin: 0, fontSize: 13 }}
      >
        {source.content}
      </Paragraph>
      {previewable && (
        <DocPreviewDrawer
          open={previewOpen}
          name={source.workspace!}
          documentID={source.document_id!}
          documentTitle={source.document_title}
          onClose={() => setPreviewOpen(false)}
        />
      )}
    </div>
  );
};

export const WorkspaceQueryResult = ({ result }: WorkspaceQueryResultProps) => (
  <>
    <Divider style={{ margin: '0 0 16px' }} />
    <div
      style={{
        background: '#f6ffed',
        border: '1px solid #b7eb8f',
        // 结果卡片语义，与 token.borderRadiusLG 对齐
        borderRadius: 12,
        padding: 16,
        marginBottom: 12,
      }}
    >
      <Text
        strong
        style={{ display: 'block', marginBottom: 8, fontSize: 13, color: '#52c41a' }}
      >
        回答
      </Text>
      <Paragraph className="long-text" style={{ margin: 0, lineHeight: 1.7 }}>{result.answer}</Paragraph>
    </div>
    {result.sources && result.sources.length > 0 && (
      <div>
        <Text strong style={{ fontSize: 13, display: 'block', marginBottom: 8 }}>
          来源文档（{result.sources.length}）
        </Text>
        <Space direction="vertical" style={{ width: '100%' }} size={8}>
          {result.sources.map((s, i) => (
            <SourceItem key={s.document_id || i} source={s} />
          ))}
        </Space>
      </div>
    )}
  </>
);
