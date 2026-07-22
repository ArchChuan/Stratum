import { message as msg } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { agentApi, conversationApi } from '../api/agent.api';
import type { Agent, ChatMessage, Conversation, ToolApproval } from '../model/agent';

import { useChatStream } from './ChatStreamContext';

import { extractErrorMessage } from '@/shared/lib/errorMessage';

const SS_AGENT = 'chat:lastAgentId';
const ssConv = (aid: string) => `chat:lastConvId:${aid}`;

export const useChatPage = () => {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [selectedAgent, setSelectedAgent] = useState<string | null>(
    () => sessionStorage.getItem(SS_AGENT),
  );
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [selectedConv, setSelectedConv] = useState<string | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [loadingConvs, setLoadingConvs] = useState(false);
	const [loadingMsgs, setLoadingMsgs] = useState(false);
	const [pendingApprovals, setPendingApprovals] = useState<ToolApproval[]>([]);
	const [approvalActionId, setApprovalActionId] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const pinnedToBottomRef = useRef(true); // auto-scroll only when user is at the bottom
  const agentIdRef = useRef(selectedAgent);
  const streamMsgIdRef = useRef<string | null>(null);
  useEffect(() => {
    agentIdRef.current = selectedAgent;
  });

  const {
    streamConversationId,
    accumulatedContent,
    streamResult,
    streamError,
		streamDone,
		streamApproval,
    startStream,
    cancelStream,
    getStreamState,
	} = useChatStream();

	useEffect(() => {
		let cancelled=false;
		agentApi.listToolApprovals().then((rows)=>{if(!cancelled)setPendingApprovals(rows)}).catch(()=>undefined);
		return()=>{cancelled=true};
	},[]);
	useEffect(()=>{if(streamApproval)setPendingApprovals((rows)=>rows.some((r)=>r.approvalId===streamApproval.approvalId)?rows:[...rows,streamApproval])},[streamApproval]);

  const streamStateRef = useRef(getStreamState);
  streamStateRef.current = getStreamState;

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const list = await agentApi.list();
        if (cancelled) return;
        setAgents(list);
        setSelectedAgent((prev) => {
          if (prev && list.some((a) => a.id === prev)) return prev;
          sessionStorage.removeItem(SS_AGENT);
          return null;
        });
      } catch {
        if (!cancelled) msg.error('加载 Agent 列表失败');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!selectedAgent) {
      setConversations([]);
      setSelectedConv(null);
      return;
    }
    sessionStorage.setItem(SS_AGENT, selectedAgent);
    let cancelled = false;
    setLoadingConvs(true);
    (async () => {
      try {
        const convs = await conversationApi.list(selectedAgent);
        if (cancelled) return;
        setConversations(convs);
        const last = sessionStorage.getItem(ssConv(selectedAgent));
        const found = convs.find((c) => c.id === last);
        setSelectedConv(found ? found.id : convs[0]?.id ?? null);
      } catch {
        if (!cancelled) msg.error('加载会话列表失败');
      } finally {
        if (!cancelled) setLoadingConvs(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedAgent]);

  useEffect(() => {
    if (!selectedConv) {
      setMessages([]);
      return;
    }
    if (agentIdRef.current) sessionStorage.setItem(ssConv(agentIdRef.current), selectedConv);
    let cancelled = false;
    setLoadingMsgs(true);
    (async () => {
      try {
        const loaded = await conversationApi.messages(selectedConv);
        if (cancelled) return;
        setMessages(loaded);

        const st = streamStateRef.current();
        if (st.conversationId === selectedConv) {
          const hasUserMsg =
            !!st.userQuery && loaded.some((m) => m.role === 'user' && m.content === st.userQuery);
          const restored: ChatMessage[] = hasUserMsg
            ? loaded
            : [
                ...loaded,
                ...(st.userQuery
                  ? [
                      {
                        id: `u-restore-${Date.now()}`,
                        role: 'user',
                        content: st.userQuery,
                        created_at: new Date().toISOString(),
                      } as ChatMessage,
                    ]
                  : []),
              ];

          if (st.streaming) {
            const msgId = `a-resume-${Date.now()}`;
            streamMsgIdRef.current = msgId;
            setSending(true);
            setMessages([
              ...restored,
              {
                id: msgId,
                role: 'assistant',
                content: st.content,
                created_at: new Date().toISOString(),
              } as ChatMessage,
            ]);
          } else if (st.done && st.content) {
            const lastLoaded = loaded[loaded.length - 1];
            const alreadySaved = lastLoaded && lastLoaded.role === 'assistant';
            if (!alreadySaved) {
              const finalContent = st.result?.output || st.content;
              setMessages([
                ...restored,
                {
                  id: `a-done-${Date.now()}`,
                  role: st.error ? 'error' : 'assistant',
                  content: st.error || finalContent,
                  created_at: new Date().toISOString(),
                  steps: st.result?.steps,
                } as ChatMessage,
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
    return () => {
      cancelled = true;
    };
  }, [selectedConv]);

  useEffect(() => {
    if (!streamMsgIdRef.current) return;
    if (streamConversationId !== selectedConv) return;
    setMessages((prev) =>
      prev.map((m) =>
        m.id === streamMsgIdRef.current ? { ...m, content: accumulatedContent } : m,
      ),
    );
  }, [accumulatedContent, streamConversationId, selectedConv]);

  useEffect(() => {
    if (!streamDone || !streamMsgIdRef.current) return;
    const msgId = streamMsgIdRef.current;
    streamMsgIdRef.current = null;
    setSending(false);
    if (streamConversationId !== selectedConv) return;
    if (streamResult) {
      const finalContent = streamResult.output || accumulatedContent;
      setMessages((prev) =>
        prev.map((m) =>
          m.id === msgId ? { ...m, content: finalContent, steps: streamResult.steps } : m,
        ),
      );
    } else if (streamError) {
      setMessages((prev) =>
        prev.map((m) => (m.id === msgId ? { ...m, role: 'error', content: streamError } : m)),
      );
    } else if (streamApproval) {
      setMessages((prev) =>
        prev.map((m) =>
          m.id === msgId ? { ...m, content: '工具调用等待审批' } : m,
        ),
      );
    }
  }, [
    streamDone,
    streamResult,
    streamError,
    streamApproval,
    streamConversationId,
    selectedConv,
    accumulatedContent,
  ]);

  const lastMsgCountRef = useRef(0);
  useEffect(() => {
    const el = bottomRef.current;
    if (!el) return;
    const newMessages = messages.length > lastMsgCountRef.current;
    lastMsgCountRef.current = messages.length;
    // On initial load or new conversation: always scroll instantly.
    if (newMessages && !sending) {
      pinnedToBottomRef.current = true;
      el.scrollIntoView({ behavior: 'instant' });
      return;
    }
    // During streaming: only scroll if user is pinned to bottom.
    if (sending && pinnedToBottomRef.current) {
      el.scrollIntoView({ behavior: 'instant' });
    }
  }, [messages, sending]);

  const handleSend = useCallback(() => {
    const text = input.trim();
    if (!text || !selectedAgent || !selectedConv) return;

    // If currently streaming, mark that message as interrupted before starting a new one.
    const prevMsgId = streamMsgIdRef.current;
    if (prevMsgId) {
      setMessages((prev) => prev.map((m) => (m.id === prevMsgId ? { ...m, interrupted: true } : m)));
      streamMsgIdRef.current = null;
    }

    const tmpId = `tmp-${Date.now()}`;
    setMessages((prev) => [
      ...prev,
      { id: tmpId, role: 'user', content: text, created_at: new Date().toISOString() } as ChatMessage,
    ]);
    setInput('');
    setSending(true);

    const msgId = `a-${Date.now()}`;
    streamMsgIdRef.current = msgId;
    setMessages((prev) => [
      ...prev,
      { id: msgId, role: 'assistant', content: '', created_at: new Date().toISOString() } as ChatMessage,
    ]);

    startStream(selectedAgent, {
      query: text,
      conversation_id: selectedConv,
      context: {},
      variables: {},
    });
  }, [input, selectedAgent, selectedConv, startStream]);

  const handleCreateConv = useCallback(async () => {
    if (!selectedAgent) return;
    try {
      const conv = await conversationApi.create(selectedAgent);
      setConversations((prev) => [conv, ...prev]);
      setSelectedConv(conv.id);
    } catch {
      msg.error('创建会话失败');
    }
  }, [selectedAgent]);

  const handleRenameConv = useCallback(async (convId: string, name: string) => {
    try {
      await conversationApi.rename(convId, name);
      setConversations((prev) => prev.map((c) => (c.id === convId ? { ...c, name } : c)));
    } catch {
      msg.error('重命名失败');
    }
  }, []);

	const handleDeleteConv = useCallback(
    async (convId: string) => {
      try {
        await conversationApi.delete(convId);
        const next = conversations.filter((c) => c.id !== convId);
        setConversations(next);
        if (selectedConv === convId) setSelectedConv(next[0]?.id ?? null);
      } catch {
        msg.error('删除会话失败');
      }
    },
    [conversations, selectedConv],
	);

	const handleApprove = useCallback(async (approvalID: string) => {
		setApprovalActionId(approvalID);
		try {
			await agentApi.decideToolApproval(approvalID, 'approved');
			const result = await agentApi.resumeToolApproval(approvalID);
			setPendingApprovals((rows) => rows.filter((row) => row.approvalId !== approvalID));
			setMessages((rows) => [
				...rows,
				{
					id: `approval-${Date.now()}`,
					role: 'assistant',
					content: result.output || '工具执行完成',
					created_at: new Date().toISOString(),
				} as ChatMessage,
			]);
			msg.success({ content: '工具执行完成', duration: 2 });
		} catch (err) {
			const detail = extractErrorMessage(err, '批准或恢复执行失败');
			const normalized = detail.toLowerCase();
			const status = normalized.includes('outcome is unknown')
				? 'unknown_outcome'
				: normalized.includes('expired')
					? 'expired'
					: normalized.includes('authorization') || normalized.includes('permission')
						? 'authorization_denied'
						: 'approved';
			setPendingApprovals((rows) => rows.map((row) => (
				row.approvalId === approvalID ? { ...row, status } : row
			)));
			msg.error({
				content: status === 'unknown_outcome' ? '工具执行结果未知，需要人工对账' : detail,
				duration: 0,
			});
		} finally {
			setApprovalActionId(null);
		}
	}, []);
	const handleReject = useCallback(async (approvalID: string) => {
		setApprovalActionId(approvalID);
		try {
			await agentApi.decideToolApproval(approvalID, 'rejected');
			setPendingApprovals((rows) => rows.filter((row) => row.approvalId !== approvalID));
			msg.success({ content: '已拒绝工具执行', duration: 2 });
		} catch (err) {
			msg.error({ content: extractErrorMessage(err, '拒绝审批失败'), duration: 0 });
		} finally {
			setApprovalActionId(null);
		}
	}, []);

  return {
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
    cancelStream,
  };
};
