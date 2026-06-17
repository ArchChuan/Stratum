import { Badge, Divider, Space, Tag, Typography } from 'antd';

import type { QueryResult } from '../model/knowledge';

const { Text, Paragraph } = Typography;

interface WorkspaceQueryResultProps {
  result: QueryResult;
}

export const WorkspaceQueryResult = ({ result }: WorkspaceQueryResultProps) => (
  <>
    <Divider style={{ margin: '0 0 16px' }} />
    <div
      style={{
        background: '#f6ffed',
        border: '1px solid #b7eb8f',
        borderRadius: 10,
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
      <Paragraph style={{ margin: 0, lineHeight: 1.7 }}>{result.answer}</Paragraph>
    </div>
    {result.sources && result.sources.length > 0 && (
      <div>
        <Text strong style={{ fontSize: 13, display: 'block', marginBottom: 8 }}>
          来源文档（{result.sources.length}）
        </Text>
        <Space direction="vertical" style={{ width: '100%' }} size={8}>
          {result.sources.map((s, i) => (
            <div
              key={i}
              style={{
                background: '#fafafa',
                border: '1px solid #f0f0f0',
                borderRadius: 8,
                padding: '10px 14px',
              }}
            >
              <Space size={8} style={{ marginBottom: 6 }}>
                <Tag style={{ margin: 0 }}>文档: {(s.document_id || '').slice(0, 8)}</Tag>
                <Badge
                  count={`${((s.score ?? 0) * 100).toFixed(1)}%`}
                  style={{ background: '#52c41a', fontSize: 11 }}
                />
              </Space>
              <Paragraph
                ellipsis={{ rows: 2 }}
                type="secondary"
                style={{ margin: 0, fontSize: 13 }}
              >
                {s.content}
              </Paragraph>
            </div>
          ))}
        </Space>
      </div>
    )}
  </>
);
