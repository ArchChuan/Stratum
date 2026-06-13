import React, { useState } from 'react';
import { Button, Select, Input, Space, Tag, Typography, Tooltip, Spin, Empty, Skeleton, Popconfirm, message as antdMsg } from 'antd';
import {
  SendOutlined, RobotOutlined, UserOutlined, PlusOutlined,
  EditOutlined, DeleteOutlined, ThunderboltOutlined, CheckOutlined,
} from '@ant-design/icons';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useChatPage } from '../hooks/useChatPage';

const { Text, Title } = Typography;
const { TextArea } = Input;

const BUBBLE = {
  user:  { maxWidth: '72%', padding: '10px 16px', borderRadius: '18px 18px 4px 18px', background: '#1677ff', color: '#fff', alignSelf: 'flex-end', fontSize: 14, lineHeight: 1.6 },
  agent: { maxWidth: '72%', padding: '10px 16px', borderRadius: '18px 18px 18px 4px', background: '#fff', border: '1px solid #f0f0f0', alignSelf: 'flex-start', fontSize: 14, lineHeight: 1.6 },
  error: { maxWidth: '72%', padding: '10px 16px', borderRadius: '18px 18px 18px 4px', background: '#fff2f0', border: '1px solid #ffccc7', alignSelf: 'flex-start', fontSize: 14, lineHeight: 1.6 },
};

const mdComponents = {
  p: ({ children }) => <p style={{ margin: '0 0 6px', lineHeight: 1.7, wordBreak: 'break-word' }}>{children}</p>,
  code: ({ inline, children }) => inline
    ? <code style={{ background: '#f5f5f5', border: '1px solid #e8e8e8', borderRadius: 3, padding: '1px 5px', fontSize: 12, fontFamily: 'JetBrains Mono, monospace' }}>{children}</code>
    : <pre style={{ background: '#1a1a1a', color: '#e8e8e8', borderRadius: 6, padding: '10px 14px', overflowX: 'auto', fontSize: 12, lineHeight: 1.6, fontFamily: 'JetBrains Mono, monospace', margin: '6px 0' }}><code>{children}</code></pre>,
  ul: ({ children }) => <ul style={{ paddingInlineStart: 20, margin: '4px 0' }}>{children}</ul>,
  ol: ({ children }) => <ol style={{ paddingInlineStart: 20, margin: '4px 0' }}>{children}</ol>,
  li: ({ children }) => <li style={{ marginBottom: 2 }}>{children}</li>,
  blockquote: ({ children }) => <blockquote style={{ borderLeft: '3px solid #d9d9d9', paddingLeft: 12, margin: '6px 0', color: '#595959' }}>{children}</blockquote>,
  a: ({ href, children }) => <a href={href} target="_blank" rel="noreferrer" style={{ color: '#1677ff' }}>{children}</a>,
  strong: ({ children }) => <strong style={{ fontWeight: 600 }}>{children}</strong>,
  h1: ({ children }) => <h1 style={{ fontSize: 18, fontWeight: 600, margin: '8px 0 4px' }}>{children}</h1>,
  h2: ({ children }) => <h2 style={{ fontSize: 16, fontWeight: 600, margin: '8px 0 4px' }}>{children}</h2>,
  h3: ({ children }) => <h3 style={{ fontSize: 14, fontWeight: 600, margin: '6px 0 4px' }}>{children}</h3>,
  table: ({ children }) => <table style={{ borderCollapse: 'collapse', width: '100%', fontSize: 13, margin: '6px 0' }}>{children}</table>,
  th: ({ children }) => <th style={{ border: '1px solid #e8e8e8', padding: '4px 10px', background: '#fafafa', fontWeight: 600, textAlign: 'left' }}>{children}</th>,
  td: ({ children }) => <td style={{ border: '1px solid #e8e8e8', padding: '4px 10px' }}>{children}</td>,
};

const StepList = ({ steps }) => !steps?.length ? null : (
  <div style={{ marginTop: 8, paddingTop: 8, borderTop: '1px solid #f0f0f0', fontSize: 12, color: '#595959' }}>
    <b style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: 0.5 }}>执行步骤</b>
    <ol style={{ margin: '4px 0 0', paddingInlineStart: 20 }}>
      {steps.map((s, i) => (
        <li key={i}><b>步骤 {s.iteration}</b>：{s.action}{s.tool && <Tag style={{ marginLeft: 4, fontSize: 10 }}>{s.tool}</Tag>}</li>
      ))}
    </ol>
  </div>
);

export default function AgentChatPage() {
  const {
    agents, selectedAgent, setSelectedAgent,
    conversations, loadingConvs, selectedConv, setSelectedConv,
    messages, loadingMsgs, sending, input, setInput, bottomRef,
    handleSend, handleCreateConv, handleRenameConv, handleDeleteConv,
  } = useChatPage();

  const [editingId, setEditingId] = useState(null);
  const [editingName, setEditingName] = useState('');

  const agentObj = agents.find(a => a.id === selectedAgent);

  const commitRename = async (convId) => {
    const name = editingName.trim();
    if (name) await handleRenameConv(convId, name);
    setEditingId(null);
  };

  return (
    <div style={{ display: 'flex', height: 'calc(100vh - 56px - 48px)', background: '#f5f5f5' }}>
      {/* Sidebar */}
      <div style={{ width: 220, background: '#fff', borderRight: '1px solid #f0f0f0', display: 'flex', flexDirection: 'column', flexShrink: 0 }}>
        <div style={{ padding: '16px 12px 12px' }}>
          <Title level={5} style={{ margin: '0 0 10px', fontSize: 14 }}>Agent 对话</Title>
          <Select
            style={{ width: '100%' }}
            placeholder="选择 Agent"
            value={selectedAgent}
            onChange={setSelectedAgent}
            options={agents.map(a => ({ value: a.id, label: a.name }))}
            size="small"
          />
        </div>
        <div style={{ padding: '0 12px 8px' }}>
          <Button icon={<PlusOutlined />} size="small" block onClick={handleCreateConv} disabled={!selectedAgent}>
            新建会话
          </Button>
        </div>
        <div style={{ flex: 1, overflowY: 'auto', padding: '4px 8px' }}>
          {loadingConvs ? <Skeleton active paragraph={{ rows: 4 }} style={{ padding: 8 }} /> : conversations.map(c => (
            <div
              key={c.id}
              onClick={() => setSelectedConv(c.id)}
              style={{
                display: 'flex', alignItems: 'center', gap: 4, padding: '6px 8px',
                borderRadius: 6, cursor: 'pointer', marginBottom: 2,
                background: c.id === selectedConv ? '#e6f4ff' : 'transparent',
              }}
            >
              {editingId === c.id ? (
                <Input
                  size="small"
                  autoFocus
                  value={editingName}
                  onChange={e => setEditingName(e.target.value)}
                  onPressEnter={() => commitRename(c.id)}
                  onBlur={() => commitRename(c.id)}
                  style={{ flex: 1, fontSize: 12 }}
                />
              ) : (
                <div style={{ flex: 1, minWidth: 0 }}>
                <Text ellipsis style={{ display: 'block', fontSize: 13, color: c.id === selectedConv ? '#1677ff' : undefined }}>{c.name}</Text>
                {c.id === selectedConv && (
                  <Tooltip title={c.id}>
                    <Text
                      type="secondary"
                      ellipsis
                      style={{ display: 'block', fontSize: 10, fontFamily: 'monospace', cursor: 'pointer' }}
                      onClick={e => {
                        e.stopPropagation();
                        navigator.clipboard.writeText(c.id).then(() => antdMsg.success('会话 ID 已复制'));
                      }}
                    >{c.id}</Text>
                  </Tooltip>
                )}
              </div>
              )}
              {c.id === selectedConv && editingId !== c.id && (
                <Space size={2}>
                  <Tooltip title="重命名">
                    <Button type="text" size="small" icon={<EditOutlined style={{ fontSize: 11 }} />}
                      onClick={e => { e.stopPropagation(); setEditingId(c.id); setEditingName(c.name); }} />
                  </Tooltip>
                  <Popconfirm title="删除此会话？" okText="删除" cancelText="取消" okType="danger"
                    onConfirm={e => { e?.stopPropagation(); handleDeleteConv(c.id); }}
                    onCancel={e => e?.stopPropagation()}>
                    <Button type="text" size="small" danger icon={<DeleteOutlined style={{ fontSize: 11 }} />}
                      onClick={e => e.stopPropagation()} />
                  </Popconfirm>
                </Space>
              )}
              {editingId === c.id && (
                <Button type="text" size="small" icon={<CheckOutlined style={{ fontSize: 11, color: '#52c41a' }} />}
                  onClick={() => commitRename(c.id)} />
              )}
            </div>
          ))}
          {!loadingConvs && conversations.length === 0 && selectedAgent && (
            <Text type="secondary" style={{ fontSize: 12, padding: '8px', display: 'block', textAlign: 'center' }}>暂无会话</Text>
          )}
        </div>
      </div>

      {/* Right panel */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        {/* Agent info strip */}
        <div style={{ height: 48, background: '#fff', borderBottom: '1px solid #f0f0f0', display: 'flex', alignItems: 'center', padding: '0 20px', gap: 10, flexShrink: 0 }}>
          <RobotOutlined style={{ fontSize: 18, color: '#1677ff' }} />
          <Text strong style={{ fontSize: 15 }}>{agentObj?.name || '请选择 Agent'}</Text>
          {agentObj?.model && <Tag color="blue" style={{ fontSize: 11 }}>{agentObj.model}</Tag>}
          {agentObj?.description && <Text type="secondary" style={{ fontSize: 12 }} ellipsis>{agentObj.description}</Text>}
        </div>

        {/* Messages */}
        <div style={{ flex: 1, overflowY: 'auto', padding: '20px 24px', display: 'flex', flexDirection: 'column', gap: 12 }}>
          {!selectedConv && !loadingMsgs && (
            <Empty description={selectedAgent ? '新建或选择一个会话' : '请先选择 Agent'} style={{ marginTop: 80 }} />
          )}
          {loadingMsgs && <Skeleton active paragraph={{ rows: 6 }} />}
          {!loadingMsgs && messages.map(m => (
            <div key={m.id} style={{ display: 'flex', flexDirection: 'column', alignItems: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
                {m.role !== 'user' && (m.role === 'agent' ? <RobotOutlined style={{ color: '#1677ff' }} /> : <ThunderboltOutlined style={{ color: '#ff4d4f' }} />)}
                <Text type="secondary" style={{ fontSize: 11 }}>{m.role === 'user' ? '你' : m.role === 'agent' ? 'Agent' : '错误'}</Text>
                {m.role === 'user' && <UserOutlined style={{ color: '#8c8c8c' }} />}
              </div>
              <div style={BUBBLE[m.role] || BUBBLE.agent}>
                {m.role === 'user'
                  ? <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{m.content}</span>
                  : <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>{m.content}</ReactMarkdown>
                }
                {m.role === 'agent' && <StepList steps={m.steps} />}
              </div>
            </div>
          ))}
          {sending && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, alignSelf: 'flex-start' }}>
              <RobotOutlined style={{ color: '#1677ff' }} />
              <Spin size="small" />
              <Text type="secondary" style={{ fontSize: 12 }}>Agent 正在处理…</Text>
            </div>
          )}
          <div ref={bottomRef} />
        </div>

        {/* Input */}
        <div style={{ padding: '12px 24px 16px', background: '#fff', borderTop: '1px solid #f0f0f0', flexShrink: 0 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
            <TextArea
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); } }}
              placeholder={selectedConv ? '输入消息，Enter 发送，Shift+Enter 换行' : '请先选择会话'}
              autoSize={{ minRows: 1, maxRows: 5 }}
              disabled={!selectedConv || sending}
              style={{ flex: 1, resize: 'none', fontSize: 14 }}
            />
            <Button
              type="primary"
              icon={<SendOutlined />}
              onClick={handleSend}
              loading={sending}
              disabled={!selectedConv || !input.trim()}
            >
              发送
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
