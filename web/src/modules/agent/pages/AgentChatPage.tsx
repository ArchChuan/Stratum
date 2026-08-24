import { Alert, Button, Drawer, Modal, Space, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { ChatComposer } from '../components/ChatComposer';
import { ChatConversationSidebar } from '../components/ChatConversationSidebar';
import { ChatHeader } from '../components/ChatHeader';
import { ChatMessageList } from '../components/ChatMessageList';
import { useChatPage } from '../hooks/useChatPage';

import { useAuth, useTenantRole } from '@/modules/iam';
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
  const { user } = useAuth();
  const {
    agents,
    agentsError,
    agentsLoading,
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
    waitingApproval,
    terminalApproval,
    resumeBlocked,
    resumeBlockedLabel,
    streaming,
    delegateStatus,
    manualResumeWaiting,
    cancelWaitingApproval,
    resumeTerminal,
		streamFailure,
		contentSwitching,
  } = useChatPage({ fixedAgentId });

  const agentObj = agents.find((a) => a.id === selectedAgent);
  // 正常等待态(waitingApproval)与终态护栏转手动(terminalApproval)共用审批卡片:
  // 终态卡片展示终态文案,护栏触发时提供「让 Agent 继续」手动入口。
  const pendingApproval = waitingApproval ?? terminalApproval;
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
			agentsLoading={agentsLoading}
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
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 0 }}>
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
          delegateStatus={delegateStatus}
        />
        {pendingApproval && (
          <ApprovalGate
            approval={pendingApproval}
            isMobile={isMobile}
            streaming={streaming}
            blocked={resumeBlocked}
            blockedLabel={resumeBlockedLabel}
            onResume={manualResumeWaiting}
            onResumeTerminal={resumeTerminal}
            onCancel={cancelWaitingApproval}
            userSub={user?.sub}
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
  isMobile: boolean;
  streaming: boolean;
  blocked: boolean;
  blockedLabel?: string;
  onResume: () => void;
  onResumeTerminal: () => void;
  onCancel: () => void;
  userSub?: string;
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
  const invalidated = approval.status === 'voided' ||
    approval.status === 'invalidated';
  // cancelled 独立显示「已取消」(产品 review ③);带级联失效原因(会话删除/策略变更
  // 等)时优先显示失效原因,不混入「已过期」时钟判定。
  if (approval.status === 'cancelled') {
    const reason = approval.invalidationReason
      ? `：${APPROVAL_INVALIDATION_LABELS[approval.invalidationReason] || approval.invalidationReason}`
      : '';
    return { terminal: true, message: reason ? `工具审批已失效${reason}` : '工具审批已取消' };
  }
  if (invalidated) {
    const reason = approval.invalidationReason
      ? `：${APPROVAL_INVALIDATION_LABELS[approval.invalidationReason] || approval.invalidationReason}`
      : '';
    return { terminal: true, message: `工具审批已失效${reason}` };
  }
  // 被拒/已完成:终态(与 useChatPage isTerminalApproval 对齐)。rejected 触发终态
  // 续跑(LLM 收尾),卡片展示终态文案;completed 仅作对账展示。
  if (approval.status === 'rejected') {
    return { terminal: true, message: '工具审批未通过' };
  }
  if (approval.status === 'completed') {
    return { terminal: true, message: '审批已完成' };
  }
  const expired = approval.status === 'expired' ||
    (!!approval.expiresAt && new Date(approval.expiresAt).getTime() <= Date.now());
  if (expired) {
    return { terminal: true, message: '工具审批已过期' };
  }
  if (approval.status === 'approved') {
    return { terminal: false, message: `工具 ${approval.toolName} 已批准` };
  }
  return { terminal: false, message: `工具 ${approval.toolName} 等待审批` };
};

// 只读审批提示卡片:审批操作收敛到审批中心(/approvals),对话页不再打扰审批人。
// approved 态由轮询自动流式续跑;离线/自动续跑未触发时提供手动"继续执行"兜底。
const ApprovalGate = ({
  approval,
  isMobile,
  streaming,
  blocked,
  blockedLabel,
  onResume,
  onResumeTerminal,
  onCancel,
  userSub,
}: ApprovalGateProps) => {
  const navigate = useNavigate();
  const { terminal, message } = resolveApprovalGate(approval);
  const approved = approval.status === 'approved';
  // H1/H3: 自动续跑失败(工具执行失败/参数校验不过/终态多次未通过)后置阻塞态 → 卡片
  // 给出可解释文案(blockedLabel 区分场景),按钮保留为手动入口。
  const failed = approved && blocked;
  const gateMessage = failed ? blockedLabel || `工具 ${approval.toolName} 自动执行失败，可手动重试` : message;
  // 取消按钮仅发起人本人可见(pending 且 userId===当前用户)。admin 代撤走审批中心
  // 「拒绝」职责,不在此暴露。SSE 帧 userId 由 useChatPage 补 user.sub。
  const canCancel = approval.status === 'pending' && !!userSub && approval.userId === userSub;
  // 终态护栏转手动:rejected/cancelled 终态 + blocked → 提供「让 Agent 继续」入口。
  const terminalManual = terminal && blocked && !!blockedLabel && !approved;

  return (
    <Alert
      type={terminal || failed ? 'error' : 'warning'}
      showIcon
      message={gateMessage}
      description={(
        <Space direction={isMobile ? 'vertical' : 'horizontal'} wrap>
          <Typography.Text>
            风险等级：{approval.riskLevel} · Server：{approval.serverId}
          </Typography.Text>
          {!terminal && (
            <Space>
              <Button onClick={() => navigate('/approvals')}>前往审批中心</Button>
              {approved && (
                <Button type="primary" disabled={streaming} onClick={onResume}>
                  {streaming ? '自动续跑中…' : '继续执行'}
                </Button>
              )}
              {canCancel && (
                <Button
                  danger
                  disabled={streaming}
                  onClick={() =>
                    Modal.confirm({
                      title: '取消工具审批',
                      content: 'Agent 将不会执行该工具，并继续处理你的任务。',
                      okText: '确认取消',
                      okButtonProps: { danger: true },
                      cancelText: '再想想',
                      onOk: onCancel,
                    })
                  }
                >
                  取消审批
                </Button>
              )}
            </Space>
          )}
          {terminalManual && (
            <Button type="primary" disabled={streaming} onClick={onResumeTerminal}>
              {streaming ? '续跑中…' : '让 Agent 继续'}
            </Button>
          )}
        </Space>
      )}
    />
  );
};
