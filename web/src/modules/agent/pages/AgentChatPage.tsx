import { Alert, Button, Drawer, Space, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { ChatComposer } from '../components/ChatComposer';
import { ChatConversationSidebar } from '../components/ChatConversationSidebar';
import { ChatHeader } from '../components/ChatHeader';
import { ChatMessageList } from '../components/ChatMessageList';
import { useChatPage } from '../hooks/useChatPage';

import { useTenantRole } from '@/modules/iam';
import { useResponsive } from '@/shared/hooks/useResponsive';

type AgentChatPageProps = {
	fixedAgentId?: string;
	showAgentSelector?: boolean;
	embedded?: boolean;
};

export const AgentChatPage = ({
	fixedAgentId,
	showAgentSelector = true,
	embedded = false,
}: AgentChatPageProps = {}) => {
  const { isMobile } = useResponsive();
  const [conversationDrawerOpen, setConversationDrawerOpen] = useState(false);
  const { isAdmin } = useTenantRole();
  const {
    agents,
    agentsError,
    reloadAgents,
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
		streamFailure,
		contentSwitching,
  } = useChatPage({ fixedAgentId });

  const agentObj = agents.find((a) => a.id === selectedAgent);
  const pendingApproval = pendingApprovals.find(
    (item) => !item.agentId || item.agentId === selectedAgent,
  );
	const assistantModelUnavailable = !!(
		agentObj?.isSystem &&
		streamFailure?.code === 'SYSTEM_ASSISTANT_MODEL_UNAVAILABLE'
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
			showAgentSelector={showAgentSelector}
			agentsError={agentsError}
			onRetryAgents={reloadAgents}
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
			height: embedded
				? 'calc(100vh - 56px - 112px)'
				: isMobile ? 'calc(100vh - 56px)' : 'calc(100vh - 56px - 48px)',
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
        />
        <ChatMessageList
          messages={messages}
          loadingMsgs={loadingMsgs}
          loadingConvs={loadingConvs}
          sending={sending}
          selectedConv={selectedConv}
          selectedAgent={selectedAgent}
          bottomRef={bottomRef}
          scrollContainerRef={scrollContainerRef}
          pinnedToBottomRef={pinnedToBottomRef}
          isMobile={isMobile}
          contentSwitching={contentSwitching}
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
				{assistantModelUnavailable && (
					<Alert
						type="error"
						showIcon
						message={isAdmin
							? '租户尚未配置平台助手模型'
							: '租户尚未配置平台助手模型，请联系租户管理员配置'}
						action={undefined}
					/>
				)}
        <ChatComposer
          input={input}
          setInput={setInput}
          sending={sending}
          selectedConv={selectedConv}
          loading={loadingConvs}
          onSend={handleSend}
          isMobile={isMobile}
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

const APPROVAL_INVALIDATION_LABELS: Record<string, string> = {
  conversation_deleted: '会话已删除',
  policy_changed: '策略已变更',
  expired: '已过期',
};

interface ApprovalGateState {
  terminal: boolean;
  message: string;
}

// 审批状态机终态判定（D7/D8/D12）。status-based 终态优先于时钟过期：已取消/级联
// 失效的审批即使 expiresAt 已过，也应显示"已失效"而非"已过期"。pending 之外的
// 状态一律 terminal，管理员按钮隐藏。
const resolveApprovalGate = (approval: ApprovalGateProps['approval']): ApprovalGateState => {
  if (approval.status === 'unknown_outcome') {
    return { terminal: true, message: '工具执行结果未知，需要人工对账' };
  }
  if (approval.status === 'authorization_denied') {
    return { terminal: true, message: '权限已变更，工具执行已阻止' };
  }
  const invalidated = approval.status === 'cancelled' ||
    approval.status === 'voided' ||
    approval.status === 'invalidated';
  if (invalidated) {
    const reason = approval.invalidationReason
      ? `：${APPROVAL_INVALIDATION_LABELS[approval.invalidationReason] || approval.invalidationReason}`
      : '';
    return { terminal: true, message: `工具审批已失效${reason}` };
  }
  const expired = approval.status === 'expired' ||
    (!!approval.expiresAt && new Date(approval.expiresAt).getTime() <= Date.now());
  if (expired) {
    return { terminal: true, message: '工具审批已过期' };
  }
  return { terminal: false, message: `工具 ${approval.toolName} 等待审批` };
};

const ApprovalGate = ({ approval, isAdmin, isMobile, loading, onApprove, onReject }: ApprovalGateProps) => {
  const { terminal, message } = resolveApprovalGate(approval);

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
