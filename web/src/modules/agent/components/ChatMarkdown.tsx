import { memo, type CSSProperties, type ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

export const BUBBLE: Record<string, CSSProperties> = {
  user: {
    maxWidth: '72%',
    background: '#1677ff',
    color: '#fff',
    padding: '10px 14px',
    borderRadius: 12,
    fontSize: 14,
    lineHeight: 1.6,
    whiteSpace: 'pre-wrap',
    wordBreak: 'break-word',
  },
  assistant: {
    maxWidth: '72%',
    background: '#fff',
    color: '#262626',
    padding: '10px 14px',
    borderRadius: 12,
    border: '1px solid #f0f0f0',
    fontSize: 14,
    lineHeight: 1.6,
  },
  error: {
    maxWidth: '72%',
    background: '#fff2f0',
    color: '#cf1322',
    padding: '10px 14px',
    borderRadius: 12,
    border: '1px solid #ffccc7',
    fontSize: 14,
    lineHeight: 1.6,
  },
};

interface MdProps {
  children?: ReactNode;
}
interface CodeProps extends MdProps {
  inline?: boolean;
}
interface LinkProps extends MdProps {
  href?: string;
}

// 链接协议白名单：仅 http/https/mailto 可点击，其余协议渲染为纯文本，防止 js: 等注入
const SAFE_LINK_PROTOCOLS = ['http:', 'https:', 'mailto:'];

const isSafeLink = (href?: string): boolean => {
  if (!href) return false;
  try {
    const url = new URL(href, window.location.origin);
    return SAFE_LINK_PROTOCOLS.includes(url.protocol);
  } catch {
    return false;
  }
};

const mdComponents = {
  p: ({ children }: MdProps) => (
    <p style={{ margin: '4px 0', lineHeight: 1.6 }}>{children}</p>
  ),
  code: ({ inline, children }: CodeProps) =>
    inline ? (
      <code
        style={{
          background: '#f5f5f5',
          padding: '2px 6px',
          borderRadius: 4,
          fontSize: 12,
          fontFamily: 'JetBrains Mono, monospace',
        }}
      >
        {children}
      </code>
    ) : (
      <pre
        style={{
          background: '#1a1a1a',
          color: '#e8e8e8',
          borderRadius: 6,
          padding: '10px 14px',
          overflowX: 'auto',
          fontSize: 12,
          lineHeight: 1.6,
          fontFamily: 'JetBrains Mono, monospace',
          margin: '6px 0',
        }}
      >
        <code>{children}</code>
      </pre>
    ),
  ul: ({ children }: MdProps) => (
    <ul style={{ paddingInlineStart: 20, margin: '4px 0' }}>{children}</ul>
  ),
  ol: ({ children }: MdProps) => (
    <ol style={{ paddingInlineStart: 20, margin: '4px 0' }}>{children}</ol>
  ),
  li: ({ children }: MdProps) => <li style={{ marginBottom: 2 }}>{children}</li>,
  blockquote: ({ children }: MdProps) => (
    <blockquote
      style={{
        borderLeft: '3px solid #d9d9d9',
        paddingLeft: 12,
        margin: '6px 0',
        color: '#595959',
      }}
    >
      {children}
    </blockquote>
  ),
  a: ({ href, children }: LinkProps) =>
    isSafeLink(href) ? (
      <a href={href} target="_blank" rel="noreferrer" style={{ color: '#1677ff' }}>
        {children}
      </a>
    ) : (
      <span style={{ color: '#595959' }}>{children}</span>
    ),
  strong: ({ children }: MdProps) => <strong style={{ fontWeight: 600 }}>{children}</strong>,
  h1: ({ children }: MdProps) => (
    <h1 style={{ fontSize: 18, fontWeight: 600, margin: '8px 0 4px' }}>{children}</h1>
  ),
  h2: ({ children }: MdProps) => (
    <h2 style={{ fontSize: 16, fontWeight: 600, margin: '8px 0 4px' }}>{children}</h2>
  ),
  h3: ({ children }: MdProps) => (
    <h3 style={{ fontSize: 14, fontWeight: 600, margin: '6px 0 4px' }}>{children}</h3>
  ),
  table: ({ children }: MdProps) => (
    <table style={{ borderCollapse: 'collapse', width: '100%', fontSize: 13, margin: '6px 0' }}>
      {children}
    </table>
  ),
  th: ({ children }: MdProps) => (
    <th
      style={{
        border: '1px solid #e8e8e8',
        padding: '4px 10px',
        background: '#fafafa',
        fontWeight: 600,
        textAlign: 'left',
      }}
    >
      {children}
    </th>
  ),
  td: ({ children }: MdProps) => (
    <td style={{ border: '1px solid #e8e8e8', padding: '4px 10px' }}>{children}</td>
  ),
};

// memo：流式期间历史消息 content 不变时不重跑 ReactMarkdown 解析
export const ChatMarkdown = memo(function ChatMarkdown({ content }: { content: string }) {
  return (
    <div className="chat-markdown" style={{ overflowWrap: 'anywhere', minWidth: 0 }}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
        {content}
      </ReactMarkdown>
    </div>
  );
});
