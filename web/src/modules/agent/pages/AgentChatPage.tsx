import { Alert, Button, Drawer, Space, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { ChatComposer } from '../components/ChatComposer';
import { ChatConversationSidebar } from '../components/ChatConversationSidebar';
import { ChatHeader } from '../components/ChatHeader';
import { ChatMessageList } from '../components/ChatMessageList';
import { SystemAssistantModelModal } from '../components/SystemAssistantModelModal';
import { useChatPage } from '../hooks/useChatPage';

import { useTenantRole } from '@/modules/iam';
import { useResponsive } from '@/shared/hooks/useResponsive';

export const AgentChatPage = () => {
  const { isMobile } = useResponsive();
  const [conversationDrawerOpen, setConversationDrawerOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const { isAdmin } = useTenantRole();
  const {
    agents,
    selectedAgent,
    setSelectedAgent,
    conversations,
    loadingConvs,
    selectedConv,
    setSelectedConv,
    messages,
    loadingMsgs,
    sending,
    input,
    setInput,
    bottomRef,
    scrollContainerRef,
    pinnedToBottomRef,
    handleSend,
    handleCreateConv,
    handleRenameConv,
    handleDeleteConv,
    pendingApprovals,
    approvalActionId,
    handleApprove,
    handleReject,
    updateSystemAssistantModel,
  } = useChatPage();

  const agentObj = agents.find((a) => a.id === selectedAgent);
  const pendingApproval = pendingApprovals.find(
    (item) => !item.agentId || item.agentId === selectedAgent,
  );
  const sidebar = (
    <ChatConversationSidebar
      agents={agents}
      selectedAgent={selectedAgent}
      onSelectAgent={setSelectedAgent}
      conversations={conversations}
      loadingConvs={loadingConvs}
      selectedConv={selectedConv}
      onSelectConv={(id) => {
        setSelectedConv(id);
        if (isMobile) setConversationDrawerOpen(false);
      }}
      onCreate={handleCreateConv}
      onRename={handleRenameConv}
      onDelete={handleDeleteConv}
      fluid={isMobile}
    />
  );

  useEffect(() => {
    if (!isMobile) setConversationDrawerOpen(false);
  }, [isMobile]);

  return (
    <div
      className="agent-chat-page"
      style={{
        display: 'flex',
        height: isMobile ? 'calc(100vh - 56px)' : 'calc(100vh - 56px - 48px)',
        maxHeight: isMobile ? 'calc(100dvh - 56px)' : undefined,
        background: '#f5f5f5',
        overflow: 'hidden',
      }}
    >
      {isMobile ? (
        <Drawer
          open={conversationDrawerOpen}
          onClose={() => setConversationDrawerOpen(false)}
          placement="left"
          width="min(360px, 100vw)"
          styles={{ body: { padding: 0, overflow: 'hidden' } }}
          destroyOnHidden
          title="会话列表"
        >
          {sidebar}
        </Drawer>
      ) : sidebar}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <ChatHeader
          agent={agentObj}
          isMobile={isMobile}
          onOpenConversations={() => setConversationDrawerOpen(true)}
          isAdmin={isAdmin}
          onOpenSettings={agentObj?.isSystem ? () => setSettingsOpen(true) : undefined}
        />
        <ChatMessageList
          messages={messages}
          loadingMsgs={loadingMsgs}
          sending={sending}
          selectedConv={selectedConv}
          selectedAgent={selectedAgent}
          bottomRef={bottomRef}
          scrollContainerRef={scrollContainerRef}
          pinnedToBottomRef={pinnedToBottomRef}
          isMobile={isMobile}
        />
        {pendingApproval && (
          <ApprovalGate
            approval={pendingApproval}
            isAdmin={isAdmin}
            isMobile={isMobile}
            loading={approvalActionId === pendingApproval.approvalId}
            onApprove={handleApprove}
            onReject={handleReject}
          />
        )}
        <ChatComposer
          input={input}
          setInput={setInput}
          sending={sending}
          selectedConv={selectedConv}
          onSend={handleSend}
          isMobile={isMobile}
        />
        <SystemAssistantModelModal
          open={settingsOpen}
          onClose={() => setSettingsOpen(false)}
          onSaved={updateSystemAssistantModel}
        />
      </div>
    </div>
  );
};

type ApprovalGateProps = {
  approval: ReturnType<typeof useChatPage>['pendingApprovals'][number];
  isAdmin: boolean;
  isMobile: boolean;
  loading: boolean;
  onApprove: (approvalId: string) => void;
  onReject: (approvalId: string) => void;
};

const ApprovalGate = ({ approval, isAdmin, isMobile, loading, onApprove, onReject }: ApprovalGateProps) => {
  const expired = approval.status === 'expired' ||
    (!!approval.expiresAt && new Date(approval.expiresAt).getTime() <= Date.now());
  const unknown = approval.status === 'unknown_outcome';
  const blocked = approval.status === 'authorization_denied';
  const terminal = expired || unknown || blocked;
  const message = unknown
    ? '工具执行结果未知，需要人工对账'
    : expired
      ? '工具审批已过期'
      : blocked
        ? '权限已变更，工具执行已阻止'
        : `工具 ${approval.toolName} 等待审批`;

  return (
    <Alert
      type={terminal ? 'error' : 'warning'}
      showIcon
      message={message}
      description={(
        <Space direction={isMobile ? 'vertical' : 'horizontal'} wrap>
          <Typography.Text>
            风险等级：{approval.riskLevel} · Server：{approval.serverId}
          </Typography.Text>
          {!terminal && !isAdmin && <Typography.Text type="secondary">需要租户管理员处理</Typography.Text>}
          {!terminal && isAdmin && (
            <Space>
              <Button
                type="primary"
                danger
                loading={loading}
                onClick={() => onApprove(approval.approvalId)}
              >
                批准并继续
              </Button>
              <Button
                aria-label="拒绝"
                disabled={loading}
                onClick={() => onReject(approval.approvalId)}
              >
                拒绝
              </Button>
            </Space>
          )}
        </Space>
      )}
    />
  );
};
