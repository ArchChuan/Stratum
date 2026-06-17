import { ChatComposer } from '../components/ChatComposer';
import { ChatConversationSidebar } from '../components/ChatConversationSidebar';
import { ChatHeader } from '../components/ChatHeader';
import { ChatMessageList } from '../components/ChatMessageList';
import { useChatPage } from '../hooks/useChatPage';

export const AgentChatPage = () => {
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
    handleSend,
    handleCreateConv,
    handleRenameConv,
    handleDeleteConv,
  } = useChatPage();

  const agentObj = agents.find((a) => a.id === selectedAgent);

  return (
    <div
      style={{
        display: 'flex',
        height: 'calc(100vh - 56px - 48px)',
        background: '#f5f5f5',
      }}
    >
      <ChatConversationSidebar
        agents={agents}
        selectedAgent={selectedAgent}
        onSelectAgent={setSelectedAgent}
        conversations={conversations}
        loadingConvs={loadingConvs}
        selectedConv={selectedConv}
        onSelectConv={setSelectedConv}
        onCreate={handleCreateConv}
        onRename={handleRenameConv}
        onDelete={handleDeleteConv}
      />
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <ChatHeader agent={agentObj} />
        <ChatMessageList
          messages={messages}
          loadingMsgs={loadingMsgs}
          sending={sending}
          selectedConv={selectedConv}
          selectedAgent={selectedAgent}
          bottomRef={bottomRef}
        />
        <ChatComposer
          input={input}
          setInput={setInput}
          sending={sending}
          selectedConv={selectedConv}
          onSend={handleSend}
        />
      </div>
    </div>
  );
};
