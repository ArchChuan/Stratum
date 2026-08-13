import { message as msg } from 'antd';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

import { agentApi, conversationApi } from '../api/agent.api';
import {
  executionArtifactSchema,
  type Agent,
  type ChatMessage,
  type Conversation,
  type ToolApproval,
} from '../model/agent';

import { useChatStream } from './ChatStreamContext';

import { extractErrorMessage } from '@/shared/lib/errorMessage';

const SS_AGENT = 'chat:lastAgentId';
const ssConv = (aid: string) => `chat:lastConvId:${aid}`;
const normalizeArtifacts = (value: unknown) => {
  const parsed = executionArtifactSchema.array().safeParse(value ?? []);
  return parsed.success ? parsed.data : [];
};

// 组装临时消息（本地乐观渲染，尚未落库）
const makeMessage = (msg: {
  id: string;
  role: string;
  content: string;
  steps?: ChatMessage['steps'];
  artifacts?: ChatMessage['artifacts'];
  interrupted?: boolean;
  sources?: ChatMessage['sources'];
}): ChatMessage => ({
  id: msg.id,
  role: msg.role,
  content: msg.content,
  created_at: new Date().toISOString(),
  steps: msg.steps,
  artifacts: msg.artifacts,
  interrupted: msg.interrupted,
  sources: msg.sources,
});

type UseChatPageOptions = { fixedAgentId?: string };

export const useChatPage = ({ fixedAgentId }: UseChatPageOptions = {}) => {
  const [agents, setAgents] = useState<Agent[]>([]);
  // agents 列表加载失败标记：失败时侧栏显示错误态+重试，而非静默当作「没有 Agent」
  //（避免 agents=[] 导致下拉空、切换不了、会话列表看似消失）。
  const [agentsError, setAgentsError] = useState(false);
  // 重试计数：+1 即重新执行 agents 加载 effect。
  const [agentsReload, setAgentsReload] = useState(0);
  const [selectedAgent, setSelectedAgent] = useState<string | null>(
    () => fixedAgentId ?? sessionStorage.getItem(SS_AGENT),
  );
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [selectedConv, setSelectedConv] = useState<string | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  // 有恢复目标（fixedAgentId 或 sessionStorage 上次 agent）时，首帧即处于「会话恢复中」，
  // 避免挂载首帧渲染「请先选择会话」空态后再跳真实页面。
  const [loadingConvs, setLoadingConvs] = useState(
    () => Boolean(fixedAgentId ?? sessionStorage.getItem(SS_AGENT)),
  );
  const [loadingMsgs, setLoadingMsgs] = useState(false);
  const [pendingApprovals, setPendingApprovals] = useState<ToolApproval[]>([]);
  const [approvalActionId, setApprovalActionId] = useState<string | null>(null);
  // 会话切换遮罩：切会话/进对话页时 pathname 不变，AppShell 的 route-blank
  // 不触发；此处按 selectedConv 变化在 paint 前同步盖不透明遮罩，杜绝
  // Windows DComp 合成器把旧会话纹理投到新会话首帧（残影）。
  const [contentSwitching, setContentSwitching] = useState(false);
  const contentBlankRafRef = useRef<number>(0);
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
    streamFailure,
    startStream,
    cancelStream,
    clearStreamFailure,
    getStreamState,
  } = useChatStream();

  useEffect(() => {
    clearStreamFailure();
  }, [selectedAgent, selectedConv, clearStreamFailure]);

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
        if (fixedAgentId) {
          const assistant = await agentApi.get(fixedAgentId);
          if (cancelled) return;
          setAgents([assistant]);
          setSelectedAgent(fixedAgentId);
          setAgentsError(false);
          return;
        }
        const list = await agentApi.list();
        if (cancelled) return;
        const ordered = [...list].sort((left, right) => Number(right.isSystem) - Number(left.isSystem));
        setAgents(ordered);
        // 失败不清 selectedAgent：保留上次 agent 时，会话列表照常按 selectedAgent 加载，
        // 不因 agents 列表失败而整个侧栏空白。
        setSelectedAgent((prev) => {
          if (prev && ordered.some((a) => a.id === prev)) return prev;
          const defaultAgent = ordered.find((agent) => agent.isSystem)?.id ?? null;
          if (defaultAgent) sessionStorage.setItem(SS_AGENT, defaultAgent);
          else sessionStorage.removeItem(SS_AGENT);
          return defaultAgent;
        });
        setAgentsError(false);
      } catch {
        if (!cancelled) {
          setAgentsError(true);
          msg.error({ content: '加载 Agent 列表失败', duration: 0 });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [fixedAgentId, agentsReload]);

  useEffect(() => {
    if (!selectedAgent) {
      setConversations([]);
      setSelectedConv(null);
      setLoadingConvs(false);
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
        if (!cancelled) msg.error({ content: '加载会话列表失败', duration: 0 });
      } finally {
        if (!cancelled) setLoadingConvs(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedAgent]);

  // 会话切换遮罩开启：useLayoutEffect 在 commit 后、paint 前同步 setState，
  // 切会话后的首帧即含不透明遮罩，合成器不会投递只含旧会话纹理的帧。
  // 遮罩由消息加载完成后的双 rAF 移除（见下方 messages effect 的 finally）。
  useLayoutEffect(() => {
    if (!selectedConv) return;
    setContentSwitching(true);
    // 该 effect 重新执行（selectedConv 再变）时先 cancel 未决 rAF，
    // 避免旧遮罩在错误时机被移除。
    return () => {
      if (contentBlankRafRef.current) cancelAnimationFrame(contentBlankRafRef.current);
    };
  }, [selectedConv]);

  useEffect(() => {
    if (!selectedConv) {
      setMessages([]);
      setContentSwitching(false);
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
                  ? [makeMessage({ id: `u-restore-${Date.now()}`, role: 'user', content: st.userQuery })]
                  : []),
              ];

          if (st.streaming) {
            const msgId = `a-resume-${Date.now()}`;
            streamMsgIdRef.current = msgId;
            setSending(true);
            setMessages([
              ...restored,
              makeMessage({ id: msgId, role: 'assistant', content: st.content }),
            ]);
          } else if (st.done && st.content) {
            const lastLoaded = loaded[loaded.length - 1];
            const alreadySaved = lastLoaded && lastLoaded.role === 'assistant';
            if (!alreadySaved) {
              const finalContent = st.result?.output || st.content;
              setMessages([
                ...restored,
                makeMessage({
                  id: `a-done-${Date.now()}`,
                  role: st.error ? 'error' : 'assistant',
                  content: st.error || finalContent,
                  steps: st.result?.steps,
                  artifacts: normalizeArtifacts(st.result?.artifacts),
                  // 滚动升级期旧后端无 sources 字段：?? [] 容错
                  sources: st.result?.sources ?? [],
                }),
              ]);
            } else if (!hasUserMsg && st.userQuery) {
              setMessages(restored);
            }
          } else if (!hasUserMsg && st.userQuery) {
            setMessages(restored);
          }
        }
      } catch {
        if (!cancelled) msg.error({ content: '加载消息历史失败', duration: 0 });
      } finally {
        if (!cancelled) setLoadingMsgs(false);
      }
      if (cancelled) {
        // 会话已切走：让新的 selectedConv effect 接管遮罩，本 effect 不再 reveal。
        return;
      }
      // 双 rAF 后再移除遮罩：消息已 set、新纹理至少被合成一帧。
      // 此时移除不会再生残影，与 AppShell route-blank 同构。
      contentBlankRafRef.current = requestAnimationFrame(() => {
        contentBlankRafRef.current = requestAnimationFrame(() => {
          contentBlankRafRef.current = 0;
          setContentSwitching(false);
        });
      });
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
          m.id === msgId
            ? {
                ...m,
                content: finalContent,
                steps: streamResult.steps,
                artifacts: normalizeArtifacts(streamResult.artifacts),
                // P1.2: done payload 的 sources（旧后端无此字段，?? [] 容错）
                sources: streamResult.sources ?? [],
              }
            : m,
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
    setMessages((prev) => [...prev, makeMessage({ id: tmpId, role: 'user', content: text })]);
    setInput('');
    setSending(true);

    const msgId = `a-${Date.now()}`;
    streamMsgIdRef.current = msgId;
    setMessages((prev) => [...prev, makeMessage({ id: msgId, role: 'assistant', content: '' })]);

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
      msg.error({ content: '创建会话失败', duration: 0 });
    }
  }, [selectedAgent]);

  const handleRenameConv = useCallback(async (convId: string, name: string) => {
    try {
      await conversationApi.rename(convId, name);
      setConversations((prev) => prev.map((c) => (c.id === convId ? { ...c, name } : c)));
    } catch {
      msg.error({ content: '重命名失败', duration: 0 });
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
        msg.error({ content: '删除会话失败', duration: 0 });
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
        makeMessage({ id: `approval-${Date.now()}`, role: 'assistant', content: result.output || '工具执行完成' }),
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

  const reloadAgents = useCallback(() => setAgentsReload((n) => n + 1), []);

  return {
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
    clearStreamFailure,
    cancelStream,
    contentSwitching,
  };
};
