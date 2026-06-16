import { useState, useEffect, useCallback, useRef } from 'react';
import { message as msg } from 'antd';
import {
  getAllAgents,
  listConversations, createConversation, renameConversation, deleteConversation,
  listMessages,
} from '../services/api';
import { useChatStream } from '../contexts/ChatStreamContext';

const SS_AGENT = 'chat:lastAgentId';
const ssConv = (aid) => `chat:lastConvId:${aid}`;

export function useChatPage() {
  const [agents, setAgents] = useState([]);
  const [selectedAgent, setSelectedAgent] = useState(() => sessionStorage.getItem(SS_AGENT) || null);
  const [conversations, setConversations] = useState([]);
  const [selectedConv, setSelectedConv] = useState(null);
  const [messages, setMessages] = useState([]);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [loadingConvs, setLoadingConvs] = useState(false);
  const [loadingMsgs, setLoadingMsgs] = useState(false);
  const bottomRef = useRef(null);
  const agentIdRef = useRef(selectedAgent);
  const streamMsgIdRef = useRef(null);
  useEffect(() => { agentIdRef.current = selectedAgent; });

  const {
    streaming, streamConversationId, accumulatedContent,
    streamResult, streamError, streamDone,
    startStream, cancelStream, getStreamState,
  } = useChatStream();

  const streamStateRef = useRef(null);
  streamStateRef.current = getStreamState;

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const r = await getAllAgents();
        if (!cancelled) {
          const list = r.data.agents || [];
          setAgents(list);
          setSelectedAgent(prev => {
            if (prev && list.some(a => a.id === prev)) return prev;
            sessionStorage.removeItem(SS_AGENT);
            return null;
          });
        }
      } catch {
        if (!cancelled) msg.error('加载 Agent 列表失败');
      }
    })();
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!selectedAgent) { setConversations([]); setSelectedConv(null); return; }
    sessionStorage.setItem(SS_AGENT, selectedAgent);
    let cancelled = false;
    setLoadingConvs(true);
    (async () => {
      try {
        const r = await listConversations(selectedAgent);
        if (cancelled) return;
        const convs = r.data.conversations || [];
        setConversations(convs);
        const last = sessionStorage.getItem(ssConv(selectedAgent));
        const found = convs.find(c => c.id === last);
        setSelectedConv(found ? found.id : (convs[0]?.id ?? null));
      } catch {
        if (!cancelled) msg.error('加载会话列表失败');
      } finally {
        if (!cancelled) setLoadingConvs(false);
      }
    })();
    return () => { cancelled = true; };
  }, [selectedAgent]);

  useEffect(() => {
    if (!selectedConv) { setMessages([]); return; }
    if (agentIdRef.current) sessionStorage.setItem(ssConv(agentIdRef.current), selectedConv);
    let cancelled = false;
    setLoadingMsgs(true);
    (async () => {
      try {
        const r = await listMessages(selectedConv);
        if (cancelled) return;
        const loaded = r.data.messages || [];
        setMessages(loaded);
        // Restore streaming state after messages load (page navigated back)
        const st = streamStateRef.current();
        if (st.conversationId === selectedConv) {
          // Restore user message if backend hasn't persisted it yet
          const hasUserMsg = st.userQuery && loaded.some(m => m.role === 'user' && m.content === st.userQuery);
          const restored = hasUserMsg ? loaded : [
            ...loaded,
            ...(st.userQuery ? [{ id: `u-restore-${Date.now()}`, role: 'user', content: st.userQuery, created_at: new Date().toISOString() }] : []),
          ];

          if (st.streaming) {
            const msgId = `a-resume-${Date.now()}`;
            streamMsgIdRef.current = msgId;
            setSending(true);
            setMessages([
              ...restored,
              { id: msgId, role: 'agent', content: st.content, created_at: new Date().toISOString() },
            ]);
          } else if (st.done && st.content) {
            const lastLoaded = loaded[loaded.length - 1];
            const alreadySaved = lastLoaded && lastLoaded.role === 'agent';
            if (!alreadySaved) {
              const finalContent = st.result?.output || st.content;
              setMessages([
                ...restored,
                { id: `a-done-${Date.now()}`, role: st.error ? 'error' : 'agent', content: st.error || finalContent, created_at: new Date().toISOString(), steps: st.result?.steps },
              ]);
            } else if (!hasUserMsg && st.userQuery) {
              setMessages(restored);
            }
          } else if (!hasUserMsg && st.userQuery) {
            setMessages(restored);
          }
        }
      } catch {
        if (!cancelled) msg.error('加载消息历史失败');
      } finally {
        if (!cancelled) setLoadingMsgs(false);
      }
    })();
    return () => { cancelled = true; };
  }, [selectedConv]); // eslint-disable-line react-hooks/exhaustive-deps

  // Sync accumulated content into the streaming message
  useEffect(() => {
    if (!streamMsgIdRef.current) return;
    if (streamConversationId !== selectedConv) return;
    setMessages(prev =>
      prev.map(m => m.id === streamMsgIdRef.current ? { ...m, content: accumulatedContent } : m)
    );
  }, [accumulatedContent, streamConversationId, selectedConv]);

  // Handle stream completion
  useEffect(() => {
    if (!streamDone || !streamMsgIdRef.current) return;
    if (streamConversationId !== selectedConv) return;

    const msgId = streamMsgIdRef.current;
    if (streamResult) {
      const finalContent = streamResult.output || accumulatedContent;
      setMessages(prev =>
        prev.map(m => m.id === msgId ? { ...m, content: finalContent, steps: streamResult.steps } : m)
      );
    } else if (streamError) {
      setMessages(prev =>
        prev.map(m => m.id === msgId ? { ...m, role: 'error', content: streamError } : m)
      );
    }
    streamMsgIdRef.current = null;
    setSending(false);
  }, [streamDone, streamResult, streamError, streamConversationId, selectedConv, accumulatedContent]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'instant' });
  }, [messages, sending]);

  const handleSend = useCallback(() => {
    const text = input.trim();
    if (!text || !selectedAgent || !selectedConv || sending) return;
    const tmpId = `tmp-${Date.now()}`;
    setMessages(prev => [...prev, { id: tmpId, role: 'user', content: text, created_at: new Date().toISOString() }]);
    setInput('');
    setSending(true);

    const msgId = `a-${Date.now()}`;
    streamMsgIdRef.current = msgId;
    setMessages(prev => [...prev, { id: msgId, role: 'agent', content: '', created_at: new Date().toISOString() }]);

    startStream(selectedAgent, { query: text, conversation_id: selectedConv, context: {}, variables: {} });
  }, [input, selectedAgent, selectedConv, sending, startStream]);

  const handleCreateConv = useCallback(async () => {
    if (!selectedAgent) return;
    try {
      const r = await createConversation(selectedAgent);
      setConversations(prev => [r.data, ...prev]);
      setSelectedConv(r.data.id);
    } catch {
      msg.error('创建会话失败');
    }
  }, [selectedAgent]);

  const handleRenameConv = useCallback(async (convId, name) => {
    try {
      await renameConversation(convId, name);
      setConversations(prev => prev.map(c => c.id === convId ? { ...c, name } : c));
    } catch {
      msg.error('重命名失败');
    }
  }, []);

  const handleDeleteConv = useCallback(async (convId) => {
    try {
      await deleteConversation(convId);
      const next = conversations.filter(c => c.id !== convId);
      setConversations(next);
      if (selectedConv === convId) setSelectedConv(next[0]?.id ?? null);
    } catch {
      msg.error('删除会话失败');
    }
  }, [conversations, selectedConv]);

  return {
    agents, selectedAgent, setSelectedAgent,
    conversations, loadingConvs, selectedConv, setSelectedConv,
    messages, loadingMsgs, sending, input, setInput, bottomRef,
    handleSend, handleCreateConv, handleRenameConv, handleDeleteConv,
  };
}
