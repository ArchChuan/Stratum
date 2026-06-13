import { useState, useEffect, useCallback, useRef } from 'react';
import { message as msg } from 'antd';
import {
  getAllAgents, executeAgentStream,
  listConversations, createConversation, renameConversation, deleteConversation,
  listMessages,
} from '../services/api';

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
  useEffect(() => { agentIdRef.current = selectedAgent; });

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
        if (!cancelled) setMessages(r.data.messages || []);
      } catch {
        if (!cancelled) msg.error('加载消息历史失败');
      } finally {
        if (!cancelled) setLoadingMsgs(false);
      }
    })();
    return () => { cancelled = true; };
  }, [selectedConv]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'instant' });
  }, [messages, sending]);

  const handleSend = useCallback(async () => {
    const text = input.trim();
    if (!text || !selectedAgent || !selectedConv || sending) return;
    const tmpId = `tmp-${Date.now()}`;
    setMessages(prev => [...prev, { id: tmpId, role: 'user', content: text, created_at: new Date().toISOString() }]);
    setInput('');
    setSending(true);
    try {
      const streamMsgId = `a-${Date.now()}`;
      // insert empty assistant message to stream tokens into
      setMessages(prev => [...prev, { id: streamMsgId, role: 'agent', content: '', created_at: new Date().toISOString() }]);

      await new Promise((resolve) => {
        executeAgentStream(
          selectedAgent,
          { query: text, conversation_id: selectedConv, context: {}, variables: {} },
          {
            onToken: (token) => {
              setMessages(prev =>
                prev.map(m => m.id === streamMsgId ? { ...m, content: m.content + token } : m)
              );
            },
            onDone: async (d) => {
              const finalContent = d.output || '';
              setMessages(prev =>
                prev.map(m => m.id === streamMsgId ? { ...m, content: finalContent, steps: d.steps } : m)
              );
              resolve();
            },
            onError: (err) => {
              setMessages(prev =>
                prev.map(m => m.id === streamMsgId
                  ? { ...m, role: 'error', content: err.message }
                  : m)
              );
              resolve();
            },
          }
        );
      });
    } catch (err) {
      setMessages(prev => [...prev, { id: `e-${Date.now()}`, role: 'error', content: err.message, created_at: new Date().toISOString() }]);
    } finally {
      setSending(false);
    }
  }, [input, selectedAgent, selectedConv, sending]);

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
