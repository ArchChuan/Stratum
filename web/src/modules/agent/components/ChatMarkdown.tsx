import type { CSSProperties, ReactNode } from 'react';
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
  agent: {
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
  a: ({ href, children }: LinkProps) => (
    <a href={href} target="_blank" rel="noreferrer" style={{ color: '#1677ff' }}>
      {children}
    </a>
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

export const ChatMarkdown = ({ content }: { content: string }) => (
  <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
    {content}
  </ReactMarkdown>
);
