import { ChatComposer } from '../components/ChatComposer';
import { ChatConversationSidebar } from '../components/ChatConversationSidebar';
import { ChatHeader } from '../components/ChatHeader';
import { ChatMessageList } from '../components/ChatMessageList';
import { useChatPage } from '../hooks/useChatPage';

export const AgentChatPage = () => {
  const { isMobile } = useResponsive();
  const [conversationDrawerOpen, setConversationDrawerOpen] = useState(false);
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
  } = useChatPage();

  const agentObj = agents.find((a) => a.id === selectedAgent);
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
        <ChatComposer
          input={input}
          setInput={setInput}
          sending={sending}
          selectedConv={selectedConv}
          onSend={handleSend}
          isMobile={isMobile}
        />
      </div>
    </div>
  );
};
import { Drawer } from 'antd';
import { useEffect, useState } from 'react';

import { useResponsive } from '@/shared/hooks/useResponsive';
