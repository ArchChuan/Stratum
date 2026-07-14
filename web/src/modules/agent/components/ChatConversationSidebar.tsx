import {
  CheckOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import {
  Button,
  Input,
  Popconfirm,
  Select,
  Skeleton,
  Space,
  Tooltip,
  Typography,
  message as antdMsg,
} from 'antd';
import { useState } from 'react';

import type { Agent, Conversation } from '../model/agent';

const { Text, Title } = Typography;

interface Props {
  agents: Agent[];
  selectedAgent: string | null;
  onSelectAgent: (id: string) => void;
  conversations: Conversation[];
  loadingConvs: boolean;
  selectedConv: string | null;
  onSelectConv: (id: string) => void;
  onCreate: () => void;
  onRename: (convId: string, name: string) => Promise<void> | void;
  onDelete: (convId: string) => void;
  fluid?: boolean;
}

export const ChatConversationSidebar = ({
  agents,
  selectedAgent,
  onSelectAgent,
  conversations,
  loadingConvs,
  selectedConv,
  onSelectConv,
  onCreate,
  onRename,
  onDelete,
  fluid = false,
}: Props) => {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingName, setEditingName] = useState('');

  const commitRename = async (convId: string) => {
    const name = editingName.trim();
    if (name) await onRename(convId, name);
    setEditingId(null);
  };

  return (
    <div
      style={{
        width: fluid ? '100%' : 220,
        height: '100%',
        background: '#fff',
        borderRight: '1px solid #f0f0f0',
        display: 'flex',
        flexDirection: 'column',
        flexShrink: 0,
      }}
    >
      <div style={{ padding: '16px 12px 12px' }}>
        <Title level={5} style={{ margin: '0 0 10px', fontSize: 14 }}>
          Agent 对话
        </Title>
        <Select
          style={{ width: '100%' }}
          placeholder="选择 Agent"
          value={selectedAgent}
          onChange={onSelectAgent}
          options={agents.map((a) => ({ value: a.id, label: a.name }))}
          size="small"
        />
      </div>
      <div style={{ padding: '0 12px 8px' }}>
        <Button
          icon={<PlusOutlined />}
          aria-label="新建会话"
          size="small"
          block
          onClick={onCreate}
          disabled={!selectedAgent}
        >
          新建会话
        </Button>
      </div>
      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 8px' }}>
        {loadingConvs ? (
          <Skeleton active paragraph={{ rows: 4 }} style={{ padding: 8 }} />
        ) : (
          conversations.map((c) => (
            <div
              key={c.id}
              onClick={(event) => {
                if ((event.target as HTMLElement).closest('button, input')) return;
                onSelectConv(c.id);
              }}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 4,
                padding: '6px 8px',
                borderRadius: 6,
                cursor: 'pointer',
                marginBottom: 2,
                background: c.id === selectedConv ? '#e6f4ff' : 'transparent',
              }}
            >
              {editingId === c.id ? (
                <Input
                  size="small"
                  autoFocus
                  value={editingName}
                  onChange={(e) => setEditingName(e.target.value)}
                  onPressEnter={() => commitRename(c.id)}
                  onBlur={() => commitRename(c.id)}
                  style={{ flex: 1, fontSize: 12 }}
                />
              ) : (
                <div style={{ flex: 1, minWidth: 0 }}>
                  <Text
                    ellipsis
                    style={{
                      display: 'block',
                      fontSize: 13,
                      color: c.id === selectedConv ? '#1677ff' : undefined,
                    }}
                  >
                    {c.name}
                  </Text>
                  {c.id === selectedConv && (
                    <Tooltip title={c.id}>
                      <Text
                        type="secondary"
                        ellipsis
                        style={{
                          display: 'block',
                          fontSize: 10,
                          fontFamily: 'monospace',
                          cursor: 'pointer',
                        }}
                        onClick={(e) => {
                          e.stopPropagation();
                          navigator.clipboard
                            .writeText(c.id)
                            .then(() => antdMsg.success('会话 ID 已复制'));
                        }}
                      >
                        {c.id}
                      </Text>
                    </Tooltip>
                  )}
                </div>
              )}
              {c.id === selectedConv && editingId !== c.id && (
                <Space size={2}>
                  <Tooltip title="重命名">
                    <Button
                      type="text"
                      size="small"
                      icon={<EditOutlined style={{ fontSize: 11 }} />}
                      aria-label="重命名"
                      onClick={(e) => {
                        e.stopPropagation();
                        setEditingId(c.id);
                        setEditingName(c.name || '');
                      }}
                    />
                  </Tooltip>
                  <Popconfirm
                    title="删除此会话？"
                    okText="删除"
                    cancelText="取消"
                    okType="danger"
                    onConfirm={(e) => {
                      e?.stopPropagation();
                      onDelete(c.id);
                    }}
                    onCancel={(e) => e?.stopPropagation()}
                  >
                    <Button
                      type="text"
                      size="small"
                      danger
                      icon={<DeleteOutlined style={{ fontSize: 11 }} />}
                      aria-label="删除"
                    />
                  </Popconfirm>
                </Space>
              )}
              {editingId === c.id && (
                <Button
                  type="text"
                  size="small"
                  icon={<CheckOutlined style={{ fontSize: 11, color: '#52c41a' }} />}
                  onClick={() => commitRename(c.id)}
                />
              )}
            </div>
          ))
        )}
        {!loadingConvs && conversations.length === 0 && selectedAgent && (
          <Text
            type="secondary"
            style={{ fontSize: 12, padding: '8px', display: 'block', textAlign: 'center' }}
          >
            暂无会话
          </Text>
        )}
      </div>
    </div>
  );
};
