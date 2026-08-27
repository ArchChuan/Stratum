import { message as msg } from 'antd';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';

import { agentApi, conversationApi } from '../api/agent.api';
import {
  executionArtifactSchema,
  type ActiveExecution,
  type Agent,
  type ChatMessage,
  type Conversation,
  type TaskSnapshot,
  type ToolApproval,
} from '../model/agent';

import { useChatStream } from './ChatStreamContext';

import { ACTIVE_EXECUTION_POLL_MS } from '@/constants';
import { useAuth } from '@/modules/iam';

const SS_AGENT = 'chat:lastAgentId';
const ssConv = (aid: string) => `chat:lastConvId:${aid}`;
// 审批终态:卡片置终态后不再轮询/自动续跑;其余状态(pending/approved/waiting_approval
// 等)视为等待态。unknown_outcome/authorization_denied 为人工对账态,暂归等待态展示。
const APPROVAL_TERMINAL_STATUSES = new Set(['rejected', 'expired', 'cancelled', 'voided', 'invalidated', 'completed']);
const isTerminalApproval = (status: string) => APPROVAL_TERMINAL_STATUSES.has(status);
// 终态审批的可解释文案:轮询/手动重试把占位气泡从「工具调用等待审批」收敛为终态提示。
const terminalApprovalLabel = (status: string): string => {
  switch (status) {
    case 'rejected':
      return '审批已拒绝';
    case 'expired':
      return '审批已过期';
    case 'cancelled':
      return '审批已取消';
    case 'invalidated':
    case 'voided':
      return '审批已失效';
    case 'completed':
      return '审批已完成';
    default:
      return `审批已${status}`;
  }
};
const normalizeArtifacts = (value: unknown) => {
  const parsed = executionArtifactSchema.array().safeParse(value ?? []);
  return parsed.success ? parsed.data : [];
};

// SSE done metadata 白名单透出（stratum_task_snapshot）：字段缺失或非对象时
// 返回 undefined，消息不带摘要条（Task 11 契约：metadata 恒存在，只按快照键判断）。
function parseTaskSnapshot(meta: Record<string, unknown> | undefined): TaskSnapshot | undefined {
  const raw = meta?.['stratum_task_snapshot'];
  if (!raw || typeof raw !== 'object') return undefined;
  const s = raw as Partial<TaskSnapshot>;
  if (!s.goal || !s.currentPhase) return undefined;
  return {
    goal: s.goal,
    currentPhase: s.currentPhase,
    completedSteps: s.completedSteps ?? [],
    nextAction: s.nextAction ?? '',
    status: s.status ?? 'active',
    failures: s.failures,
  };
}

// 组装临时消息（本地乐观渲染，尚未落库）
const makeMessage = (msg: {
  id: string;
  role: string;
  content: string;
  steps?: ChatMessage['steps'];
  artifacts?: ChatMessage['artifacts'];
  interrupted?: boolean;
  sources?: ChatMessage['sources'];
  noAnswer?: ChatMessage['noAnswer'];
  taskSnapshot?: ChatMessage['taskSnapshot'];
  factCheck?: ChatMessage['factCheck'];
}): ChatMessage => ({
  id: msg.id,
  role: msg.role,
  content: msg.content,
  created_at: new Date().toISOString(),
  steps: msg.steps,
  artifacts: msg.artifacts,
  interrupted: msg.interrupted,
  sources: msg.sources,
  noAnswer: msg.noAnswer,
  taskSnapshot: msg.taskSnapshot,
  factCheck: msg.factCheck,
});

type UseChatPageOptions = { fixedAgentId?: string };

export const useChatPage = ({ fixedAgentId }: UseChatPageOptions = {}) => {
  const { user } = useAuth();
  const [agents, setAgents] = useState<Agent[]>([]);
  // agents 列表加载失败标记：失败时侧栏显示错误态+重试，而非静默当作「没有 Agent」
  //（避免 agents=[] 导致下拉空、切换不了、会话列表看似消失）。
  const [agentsError, setAgentsError] = useState(false);
  // agents 列表加载中标记：侧栏 Select 与列表区显示 loading，避免首帧闪「暂无可用 Agent」。
  const [agentsLoading, setAgentsLoading] = useState(false);
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
  // 会话切换遮罩：切会话/进对话页时 pathname 不变，AppShell 的 route-blank
  // 不触发；此处按 selectedConv 变化在 paint 前同步盖不透明遮罩，杜绝
  // Windows DComp 合成器把旧会话纹理投到新会话首帧（残影）。
  const [contentSwitching, setContentSwitching] = useState(false);
  const contentBlankRafRef = useRef<number>(0);
  const contentBlankTimerRef = useRef<number>(0);
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const pinnedToBottomRef = useRef(true); // auto-scroll only when user is at the bottom
  const agentIdRef = useRef(selectedAgent);
  const streamMsgIdRef = useRef<string | null>(null);
  // 已发起续跑的 execution_id:轮询/刷新恢复据此跳过重复触发同一续跑。
  const resumingExecIdRef = useRef<string | null>(null);
  // H3: 自动续跑失败过的 execution_id 集合(ReleaseExecution 回滚后审批回到
  // approved,若无记忆轮询会每 ACTIVE_EXECUTION_POLL_MS 无限自动重试)。
  // 失败记入后停止自动续跑,转 manualResumeWaiting 手动入口;成功消费后移除。
  const resumeFailedExecIdsRef = useRef<Set<string>>(new Set());
  // 当前会话「自动续跑失败、需手动重试」态:供审批卡片显示可解释文案而非空转。
  const [resumeBlocked, setResumeBlocked] = useState(false);
  // 终态护栏:同一会话连续 rejected/cancelled 终态触发续跑的次数;>=2 后不再自动
  // 续跑,转手动入口(「是否让 Agent 继续?」)。会话切换重置。
  const terminalResumeCountRef = useRef(0);
  // 阻塞态可解释文案:H3 工具失败 vs 终态多次未通过 区分展示,ApprovalGate 覆盖默认。
  const [resumeBlockedLabel, setResumeBlockedLabel] = useState('');
  // 终态续跑标记:续跑成功收尾时给消息打「该工具未执行」持久痕迹(仿 interrupted)。
  const terminalResumeMarkRef = useRef(false);
  useEffect(() => {
    agentIdRef.current = selectedAgent;
  });

  const {
    streaming,
    streamConversationId,
    accumulatedContent,
    streamResult,
    streamError,
    streamDone,
    streamApproval,
    streamConflict,
    streamFailure,
    streamDelegateStatus,
    startStream,
    cancelStream,
    clearStreamFailure,
    getStreamState,
  } = useChatStream();

  useEffect(() => {
    clearStreamFailure();
    // 终态护栏会话内计数:切会话/切 agent 重置。
    terminalResumeCountRef.current = 0;
  }, [selectedAgent, selectedConv, clearStreamFailure]);

  useEffect(() => {
    let cancelled=false;
    agentApi.listToolApprovals().then((rows)=>{if(!cancelled)setPendingApprovals(rows)}).catch(()=>undefined);
    return()=>{cancelled=true};
  },[]);
  // SSE 等待审批帧并入待办:补附会话 id(发起人卡片按当前会话过滤用)。
  useEffect(() => {
    if (!streamApproval) return;
    // SSE 等待审批帧只发给发起执行流的会话本人;补附 userId(user.sub) 供对话页
    // 「取消」按钮据此仅对发起人本人展示(admin 兜底走审批中心「拒绝」职责)。
    const withConv = {
      ...streamApproval,
      // SSE 帧原始 status 为 waiting_approval,而审批卡片/「取消」按钮按 pending
      // 语义判定(AgentChatPage canCancel 要求 status === 'pending'),这里归一化,
      // 使取消按钮在刷新前即可展示(否则需等 listToolApprovals 轮询刷新)。
      status: 'pending',
      conversationId: streamConversationId || undefined,
      userId: user?.sub || streamApproval.userId,
    };
    setPendingApprovals((rows) => {
      const idx = rows.findIndex((r) => r.approvalId === withConv.approvalId);
      if (idx === -1) return [...rows, withConv];
      // 已有行(如恢复分支 listToolApprovals 先入):合并补全而非丢弃,保证
      // 身份(user.sub 异步加载就绪后)与状态归一生效;已有字段优先,避免用
      // SSE 帧的占位值(riskLevel='unclassified')覆盖更完整的恢复数据。
      const existing = rows[idx];
      const next = rows.slice();
      next[idx] = {
        ...existing,
        status: isTerminalApproval(String(existing.status)) ? existing.status : 'pending',
        conversationId: existing.conversationId || withConv.conversationId,
        userId: existing.userId || withConv.userId,
        toolName: existing.toolName || withConv.toolName,
        serverId: existing.serverId || withConv.serverId,
        riskLevel: existing.riskLevel || withConv.riskLevel,
      };
      return next;
    });
  }, [streamApproval, streamConversationId, user?.sub]);

  // 自动续跑(审批已批准 / 刷新恢复 running|paused 共用):复用现有 SSE 链路,
  // 携带 execution_id 从 checkpoint 续流;token 追加到 streamMsgIdRef 对应消息,
  // 不新增 assistant 消息、不打断上下文。
  const doApprovalResume = useCallback(
    (agentId: string, executionId: string, query: string, placeholder?: string) => {
      if (!selectedConv) return;
      resumingExecIdRef.current = executionId;
      setSending(true);
      // H2②: 刷新恢复路径(restoreFromActive waiting_approval 分支只补卡片、不建
      // 占位)时 streamMsgIdRef 可能为空;兜底建占位消息承接 token/结果,批准续跑
      // 不被 streamDone effect 的 early-return 静默吞掉。
      if (!streamMsgIdRef.current) {
        const msgId = `a-approve-${Date.now()}`;
        streamMsgIdRef.current = msgId;
        setMessages((prev) => [
          ...prev,
          makeMessage({ id: msgId, role: 'assistant', content: placeholder ?? '已批准，继续执行中…' }),
        ]);
      }
      startStream(agentId, {
        query,
        conversation_id: selectedConv,
        context: {},
        variables: {},
        execution_id: executionId,
      });
    },
    [selectedConv, startStream],
  );
  // 刷新恢复 running|paused:消息历史无本回合占位,新建占位 assistant 消息承接续跑。
  const doFreshResume = useCallback(
    (agentId: string, executionId: string, query: string) => {
      if (!selectedConv) return;
      resumingExecIdRef.current = executionId;
      setSending(true);
      const msgId = `a-restore-${Date.now()}`;
      streamMsgIdRef.current = msgId;
      setMessages((prev) => [
        ...prev,
        makeMessage({ id: msgId, role: 'assistant', content: '继续执行中…' }),
      ]);
      startStream(agentId, {
        query,
        conversation_id: selectedConv,
        context: {},
        variables: {},
        execution_id: executionId,
      });
    },
    [selectedConv, startStream],
  );
  // 刷新恢复:按后端 active-execution 分支处理。waiting_approval → 恢复审批卡片
  // (ListPending 补全工具信息);running|paused → 自动续跑。非 404 错误由调用方处理,
  // 这里只负责状态恢复,禁止静默重新执行。
  const restoreFromActive = useCallback(
    async (active: ActiveExecution, fallbackQuery: string) => {
      if (active.status === 'waiting_approval') {
        if (!active.approvalId) return;
        // 局部 const 保留窄化:参数属性访问在 setState 回调闭包内会丢失 string 窄化。
        const approvalId = active.approvalId;
        const rows = await agentApi.listToolApprovals().catch(() => []);
        const row = rows.find((r) => r.approvalId === approvalId);
        setPendingApprovals((prev) =>
          prev.some((r) => r.approvalId === approvalId)
            ? prev
            : [
                ...prev,
                row ?? {
                  approvalId,
                  agentId: active.agentId,
                  toolName: '',
                  serverId: '',
                  riskLevel: '',
                  status: active.approvalStatus || 'pending',
                  conversationId: selectedConv || undefined,
                },
              ],
        );
        return;
      }
      if (active.status === 'running' || active.status === 'paused') {
        const query = active.userQuery || fallbackQuery;
        if (!query) return;
        doFreshResume(active.agentId, active.executionId, query);
      }
    },
    [selectedConv, doFreshResume],
  );

  const streamStateRef = useRef(getStreamState);
  streamStateRef.current = getStreamState;

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setAgentsLoading(true);
      try {
        if (fixedAgentId) {
          const assistant = await agentApi.get(fixedAgentId);
          if (cancelled) return;
          setAgents([assistant]);
          setSelectedAgent(fixedAgentId);
          setAgentsError(false);
          setAgentsLoading(false);
          return;
        }
        const list = await agentApi.list();
        if (cancelled) return;
        // 等化后 agent 无平台/普通之分，按 API 列表顺序展示，默认选中首个。
        setAgents(list);
        // 失败不清 selectedAgent：保留上次 agent 时，会话列表照常按 selectedAgent 加载，
        // 不因 agents 列表失败而整个侧栏空白。
        setSelectedAgent((prev) => {
          if (prev && list.some((a) => a.id === prev)) return prev;
          // selectedAgent 为 null 会让 conversations effect 直接 return，会话列表
          // 永不加载、侧栏空白，因此回退选择列表首个 agent。
          const defaultAgent = list[0]?.id ?? null;
          if (defaultAgent) sessionStorage.setItem(SS_AGENT, defaultAgent);
          else sessionStorage.removeItem(SS_AGENT);
          return defaultAgent;
        });
        setAgentsError(false);
        setAgentsLoading(false);
      } catch {
        if (!cancelled) {
          setAgentsError(true);
          setAgentsLoading(false);
          msg.error({ content: '加载 Agent 列表失败', duration: 3 });
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
        if (!cancelled) msg.error({ content: '加载会话列表失败', duration: 3 });
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
      if (contentBlankTimerRef.current) {
        window.clearTimeout(contentBlankTimerRef.current);
        contentBlankTimerRef.current = 0;
      }
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
          } else if (st.approval) {
            // 内存流停在审批等待:恢复占位消息并保留 streamMsgIdRef,批准续跑复用。
            const msgId = `a-approval-${Date.now()}`;
            streamMsgIdRef.current = msgId;
            setSending(false);
            setMessages([
              ...restored,
              makeMessage({ id: msgId, role: 'assistant', content: '工具调用等待审批' }),
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
                  noAnswer: st.result?.noAnswer,
                  taskSnapshot: parseTaskSnapshot(st.result?.metadata),
                  // 幻觉防护对账报告（校验关/旧后端缺省）
                  factCheck: st.result?.factCheck,
                }),
              ]);
            } else if (!hasUserMsg && st.userQuery) {
              setMessages(restored);
            }
          } else if (!hasUserMsg && st.userQuery) {
            setMessages(restored);
          }
        } else {
          // 本会话无内存流(刷新/首次进入/跨标签页):后端 active-execution 是刷新
          // 连续性的权威来源。非 404 错误必须暴露,禁止当作"无执行"发起全新执行
          // (SECURITY-MEDIUM-1)。
          let active: ActiveExecution | null = null;
          try {
            active = await agentApi.getActiveExecution(selectedConv);
          } catch {
            if (!cancelled) msg.error({ content: '无法确认会话执行状态，已中止自动恢复', duration: 3 });
          }
          if (cancelled) return;
          if (active && resumingExecIdRef.current !== active.executionId) {
            const lastUserMsg = [...loaded].reverse().find((m) => m.role === 'user');
            await restoreFromActive(active, lastUserMsg?.content || '');
          }
        }
      } catch {
        if (!cancelled) msg.error({ content: '加载消息历史失败', duration: 3 });
      } finally {
        if (!cancelled) setLoadingMsgs(false);
      }
      if (cancelled) {
        // 会话已切走：让新的 selectedConv effect 接管遮罩，本 effect 不再 reveal。
        return;
      }
      // 双 rAF 后再移除遮罩：消息已 set、新纹理至少被合成一帧。
      // 此时移除不会再生残影，与 AppShell route-blank 同构；
      // setTimeout 兜底防 rAF 长期不触发（主线程忙/标签失焦）导致遮罩滞留。
      const reveal = () => {
        if (contentBlankRafRef.current) {
          cancelAnimationFrame(contentBlankRafRef.current);
          contentBlankRafRef.current = 0;
        }
        contentBlankTimerRef.current = 0;
        setContentSwitching(false);
      };
      contentBlankRafRef.current = requestAnimationFrame(() => {
        contentBlankRafRef.current = requestAnimationFrame(() => {
          contentBlankRafRef.current = 0;
          reveal();
        });
      });
      contentBlankTimerRef.current = window.setTimeout(reveal, 2000);
    })();
    return () => {
      cancelled = true;
      if (contentBlankTimerRef.current) {
        window.clearTimeout(contentBlankTimerRef.current);
        contentBlankTimerRef.current = 0;
      }
    };
    // restoreFromActive 依赖 [selectedConv, doFreshResume]（doFreshResume 又依赖
    // 稳定的 startStream），selectedConv 不变时引用稳定，不会触发本 effect 重跑。
  }, [selectedConv, restoreFromActive]);

  useEffect(() => {
    if (!streamMsgIdRef.current) return;
    if (streamConversationId !== selectedConv) return;
    setMessages((prev) =>
      prev.map((m) =>
        // || m.content:续跑首帧 content 复位为空时保留占位(审批等待/继续执行中),
        // token 到达后再回写,避免续跑瞬间清空占位。
        m.id === streamMsgIdRef.current ? { ...m, content: accumulatedContent || m.content } : m,
      ),
    );
  }, [accumulatedContent, streamConversationId, selectedConv]);

  useEffect(() => {
    if (!streamDone || !streamMsgIdRef.current) return;
    const msgId = streamMsgIdRef.current;
    // 双 tab 并发续跑败者(409):本窗口的续跑未生效,流已结束。清空在途标记,
    // 保留占位消息 id,卡片收敛与占位气泡更新交给 active-execution 轮询做败方同步。
    if (streamConflict) {
      resumingExecIdRef.current = null;
      setSending(false);
      return;
    }
    // 续跑收尾:清空在途 execution 前捕获,供失败记账/成功清理。非续跑(handleSend)
    // 时 resumingExecIdRef 恒为空,不误记。
    const finishedExecId = resumingExecIdRef.current;
    resumingExecIdRef.current = null;
    setSending(false);
    if (streamConversationId !== selectedConv) return;
    if (streamResult) {
      streamMsgIdRef.current = null;
      // H1: 续跑成功 → 该会话审批卡片收敛(移除死入口),失败记账清零。
      if (finishedExecId) {
        resumeFailedExecIdsRef.current.delete(finishedExecId);
        setResumeBlocked(false);
        setPendingApprovals((rows) => rows.filter((r) => r.conversationId !== selectedConv));
      }
      // 终态续跑(取消/被拒)成功收尾:消费标记,给占位消息打「该工具未执行」持久痕迹。
      const terminalMarked = terminalResumeMarkRef.current;
      terminalResumeMarkRef.current = false;
      const finalContent = streamResult.output || accumulatedContent;
      setMessages((prev) =>
        prev.map((m) =>
          m.id === msgId
            ? {
                ...m,
                content: finalContent,
                approvalRejected: terminalMarked || undefined,
                steps: streamResult.steps,
                artifacts: normalizeArtifacts(streamResult.artifacts),
                // P1.2: done payload 的 sources（旧后端无此字段，?? [] 容错）
                sources: streamResult.sources ?? [],
                noAnswer: streamResult.noAnswer,
                taskSnapshot: parseTaskSnapshot(streamResult.metadata),
                // 幻觉防护对账报告（校验关/旧后端缺省）
                factCheck: streamResult.factCheck,
              }
            : m,
        ),
      );
    } else if (streamError) {
      streamMsgIdRef.current = null;
      // H3: 自动续跑失败(工具执行失败/参数校验不过→后端回滚 waiting_approval) →
      // 记入失败集合并置阻塞态;轮询不再对同 execution 无限自动重试,卡片转手动入口。
      if (finishedExecId) {
        resumeFailedExecIdsRef.current.add(finishedExecId);
        setResumeBlocked(true);
      }
      setMessages((prev) =>
        prev.map((m) => (m.id === msgId ? { ...m, role: 'error', content: streamError } : m)),
      );
    } else if (streamApproval) {
      // 等待审批:保留 streamMsgIdRef,批准后续跑复用同一条消息追加 token,不新增消息。
      setMessages((prev) =>
        prev.map((m) =>
          m.id === msgId ? { ...m, content: '工具调用等待审批' } : m,
        ),
      );
    } else {
      // 用户取消等无 result/error/approval 的终态:不再复用该消息 id。
      streamMsgIdRef.current = null;
    }
  }, [
    streamDone,
    streamResult,
    streamError,
    streamApproval,
    streamConflict,
    streamConversationId,
    selectedConv,
    accumulatedContent,
  ]);

  // 当前会话的审批等待卡片(只读):ListPending 合并 SSE 帧,刷新由 active-execution
  // 恢复;approved 态由下方轮询自动续跑,离线可手动"继续执行"。
  const waitingApproval = useMemo(() => {
    if (!selectedConv) return undefined;
    return pendingApprovals.find(
      (r) => r.conversationId === selectedConv && !isTerminalApproval(String(r.status)),
    );
  }, [pendingApprovals, selectedConv]);

  // 终态审批(被拒/已取消):展示终态文案;终态护栏(连续 2 次未通过)转手动时,
  // ApprovalGate 据此提供「让 Agent 继续」入口。
  const terminalApproval = useMemo(() => {
    if (!selectedConv) return undefined;
    return pendingApprovals.find(
      (r) => r.conversationId === selectedConv && (r.status === 'rejected' || r.status === 'cancelled'),
    );
  }, [pendingApprovals, selectedConv]);

  // 审批等待态卡片:轮询 active-execution,approved → 自动流式续跑;终态 → 卡片置终态。
  useEffect(() => {
    if (!selectedConv || !waitingApproval) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const active = await agentApi.getActiveExecution(selectedConv);
        if (cancelled || !active) return;
        // H3 败方同步:执行已不在等审批(另一窗口抢占后 checkpoint 置 running,或
        // 已 completed)。移除卡片避免冻结死入口,占位气泡更新为可解释提示;本窗口
        // 自己发起的续跑(在途)由 streamDone 收尾,此处不干预。
        if (active.status !== 'waiting_approval') {
          if (resumingExecIdRef.current === active.executionId) return;
          if (active.status === 'running' || active.status === 'completed') {
            const label =
              active.status === 'completed'
                ? '该工具已在其他窗口执行完成，结果可刷新查看'
                : '该工具已在其他窗口执行中，结果将同步';
            setPendingApprovals((rows) => rows.filter((r) => r.conversationId !== selectedConv));
            if (streamMsgIdRef.current) {
              setMessages((prev) =>
                prev.map((m) =>
                  m.id === streamMsgIdRef.current ? { ...m, content: label } : m,
                ),
              );
            }
          }
          return;
        }
        if (!active.approvalId || !active.userQuery) return;
        if (active.approvalStatus === 'approved') {
          // 已有流在跑(用户新发起执行)或该续跑已在途:不抢占。
          if (streamStateRef.current().streaming) return;
          if (resumingExecIdRef.current === active.executionId) return;
          // H3: 自动续跑失败过的 execution 不再自动重试(工具执行失败回滚后仍为
          // approved,无记忆会无限循环),转 manualResumeWaiting 手动入口。
          if (resumeFailedExecIdsRef.current.has(active.executionId)) {
            setResumeBlocked(true);
            return;
          }
          doApprovalResume(active.agentId, active.executionId, active.userQuery);
          return;
        }
        if (active.approvalStatus && isTerminalApproval(active.approvalStatus)) {
          setPendingApprovals((rows) =>
            rows.map((r) =>
              r.approvalId === active.approvalId
                ? { ...r, status: active.approvalStatus as ToolApproval['status'] }
                : r,
            ),
          );
          // H1: 终态同步更新占位气泡文案,避免「工具调用等待审批」永久卡死误导。
          if (streamMsgIdRef.current) {
            const label = terminalApprovalLabel(active.approvalStatus);
            setMessages((prev) =>
              prev.map((m) =>
                m.id === streamMsgIdRef.current ? { ...m, content: label } : m,
              ),
            );
          }
          // 终态续跑(主需求):rejected/cancelled → 复用自动续跑链路,LLM 感知工具
          // 未执行后自行收尾。防重:流已在跑/该 execution 在途/H3 失败集 跳过。
          if (active.approvalStatus === 'rejected' || active.approvalStatus === 'cancelled') {
            if (streamStateRef.current().streaming) return;
            if (resumingExecIdRef.current === active.executionId) return;
            if (resumeFailedExecIdsRef.current.has(active.executionId)) {
              setResumeBlocked(true);
              return;
            }
            // 终态护栏:同一会话连续 2 次未通过后不再自动续跑,转手动入口。
            terminalResumeCountRef.current += 1;
            if (terminalResumeCountRef.current >= 2) {
              setResumeBlocked(true);
              setResumeBlockedLabel('已多次审批未通过，是否让 Agent 继续？');
              return;
            }
            terminalResumeMarkRef.current = true;
            const placeholder =
              active.approvalStatus === 'cancelled'
                ? '已取消该工具，Agent 正在收尾…'
                : '审批未通过，Agent 正在收尾…';
            doApprovalResume(active.agentId, active.executionId, active.userQuery, placeholder);
          }
        }
      } catch {
        // 非 404(DB 抖动)保持现状,下轮重试;不误判终态。
      }
    };
    const timer = window.setInterval(poll, ACTIVE_EXECUTION_POLL_MS);
    poll();
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [selectedConv, waitingApproval, doApprovalResume]);

  // 手动"继续执行"兜底(自动续跑未触发/离线时):从 active-execution 取 execution_id
  // 后复用流式续跑;已消费审批先从卡片移除。
  const manualResumeWaiting = useCallback(async () => {
    if (!selectedConv || !waitingApproval) return;
    try {
      const active = await agentApi.getActiveExecution(selectedConv);
      if (!active || !active.userQuery) {
        msg.warning({ content: '无法确认执行状态，请稍后重试', duration: 3 });
        return;
      }
      // H3 败方同步:其他窗口已抢占执行 → 提示而非触发,避免双跑。
      if (active.status === 'running' || active.status === 'completed') {
        msg.info({ content: '该执行已在其他窗口进行，无需重复操作', duration: 3 });
        return;
      }
      if (active.approvalStatus && isTerminalApproval(active.approvalStatus)) {
        msg.warning({
          content: `该审批已${terminalApprovalLabel(active.approvalStatus)}，请重新发起`,
          duration: 3,
        });
        return;
      }
      if (active.status !== 'waiting_approval' || active.approvalStatus !== 'approved') {
        msg.warning({ content: '当前审批尚未通过，无法继续执行', duration: 3 });
        return;
      }
      // 手动重试即显式授权:解除该 execution 的失败阻塞与终态护栏计数后重新续跑。
      resumeFailedExecIdsRef.current.delete(active.executionId);
      setResumeBlocked(false);
      setResumeBlockedLabel('');
      terminalResumeCountRef.current = 0;
      setPendingApprovals((rows) => rows.filter((r) => r.approvalId !== active.approvalId));
      doApprovalResume(active.agentId, active.executionId, active.userQuery);
    } catch {
      msg.error({ content: '无法确认执行状态，请稍后重试', duration: 3 });
    }
  }, [selectedConv, waitingApproval, doApprovalResume]);

  // 发起人主动取消待批审批:先同步设 in-flight 标记防双触发(轮询/按钮并发,产品
  // review 低优)→ 调后端取消(pending→cancelled)→ 乐观置卡片终态 + 场景气泡 →
  // 取 active-execution 拿 executionId 续跑(取消即工具未执行 → 主链路收尾)。
  const cancelWaitingApproval = useCallback(async () => {
    if (!selectedConv || !waitingApproval) return;
    const approvalId = waitingApproval.approvalId;
    // 已有在途续跑(如轮询刚触发 approved 自动续跑)则不再叠加标记,避免取消失败
    // 误清在途 id。
    const hadInflight = resumingExecIdRef.current !== null;
    if (!hadInflight) resumingExecIdRef.current = `cancel-${approvalId}`;
    try {
      await agentApi.cancelToolApproval(approvalId);
    } catch (err) {
      if (!hadInflight) resumingExecIdRef.current = null;
      const status = (err as { status?: number })?.status;
      if (status === 409) {
        msg.warning({ content: '该审批已被处理，无需再取消', duration: 3 });
      } else {
        msg.error({ content: '取消失败，请稍后重试', duration: 3 });
      }
      return;
    }
    setPendingApprovals((rows) =>
      rows.map((r) => (r.approvalId === approvalId ? { ...r, status: 'cancelled' } : r)),
    );
    if (streamMsgIdRef.current) {
      setMessages((prev) =>
        prev.map((m) =>
          m.id === streamMsgIdRef.current ? { ...m, content: '已取消该工具，Agent 正在收尾…' } : m,
        ),
      );
    }
    try {
      const active = await agentApi.getActiveExecution(selectedConv);
      if (
        active &&
        active.userQuery &&
        (active.status === 'waiting_approval' || active.status === 'running' || active.status === 'paused')
      ) {
        terminalResumeCountRef.current = 0;
        terminalResumeMarkRef.current = true;
        doApprovalResume(active.agentId, active.executionId, active.userQuery, '已取消该工具，Agent 正在收尾…');
      }
    } catch {
      // 取不到执行状态:卡片已置终态、占位气泡已收敛,不阻塞用户;轮询兜底。
    }
  }, [selectedConv, waitingApproval, doApprovalResume]);

  // 终态护栏转手动「让 Agent 继续」:用户显式确认后对 rejected/cancelled 执行续跑
  // (无 approved 守卫,与 manualResumeWaiting 区分)。成功收尾打持久痕迹。
  const resumeTerminal = useCallback(async () => {
    if (!selectedConv || !terminalApproval) return;
    try {
      const active = await agentApi.getActiveExecution(selectedConv);
      if (!active || !active.userQuery) {
        msg.warning({ content: '无法确认执行状态，请稍后重试', duration: 3 });
        return;
      }
      if (active.status === 'completed') {
        msg.info({ content: '该执行已完成，无需重复操作', duration: 3 });
        return;
      }
      terminalResumeCountRef.current = 0;
      setResumeBlocked(false);
      setResumeBlockedLabel('');
      setPendingApprovals((rows) => rows.filter((r) => r.conversationId !== selectedConv));
      terminalResumeMarkRef.current = true;
      doApprovalResume(active.agentId, active.executionId, active.userQuery, '审批未通过，Agent 正在收尾…');
    } catch {
      msg.error({ content: '无法确认执行状态，请稍后重试', duration: 3 });
    }
  }, [selectedConv, terminalApproval, doApprovalResume]);

  const lastMsgCountRef = useRef(0);
  const lastLoadingMsgsRef = useRef(loadingMsgs); // 与初始态(false)对齐
  useEffect(() => {
    const el = bottomRef.current;
    if (!el) return;
    const loadingJustFinished = lastLoadingMsgsRef.current && !loadingMsgs;
    lastLoadingMsgsRef.current = loadingMsgs;
    const newMessages = messages.length > lastMsgCountRef.current;
    lastMsgCountRef.current = messages.length;
    // 初始加载/新会话/刷新恢复:消息真正挂载后再锚定。刷新恢复路径 setMessages 与
    // setLoadingMsgs(false) 被 await getActiveExecution 分隔成两批——第一批
    // (loadingMsgs 仍 true)消息未挂载(Skeleton 展示),滚动无效且 newMessages 被预消费,
    // 第二批需靠 true→false 边沿补触发;双 rAF 保证 DOM 挂载完成后滚动。
    if (!loadingMsgs && (newMessages || loadingJustFinished) && !sending) {
      pinnedToBottomRef.current = true;
      requestAnimationFrame(() => requestAnimationFrame(() => {
        bottomRef.current?.scrollIntoView({ behavior: 'instant' });
      }));
      return;
    }
    // During streaming: only scroll if user is pinned to bottom.
    if (sending && pinnedToBottomRef.current && !loadingMsgs) {
      el.scrollIntoView({ behavior: 'instant' });
    }
  }, [messages, sending, loadingMsgs]);

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
      msg.error({ content: '创建会话失败', duration: 3 });
    }
  }, [selectedAgent]);

  const handleRenameConv = useCallback(async (convId: string, name: string) => {
    try {
      await conversationApi.rename(convId, name);
      setConversations((prev) => prev.map((c) => (c.id === convId ? { ...c, name } : c)));
    } catch {
      msg.error({ content: '重命名失败', duration: 3 });
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
        msg.error({ content: '删除会话失败', duration: 3 });
      }
    },
    [conversations, selectedConv],
  );

  const reloadAgents = useCallback(() => setAgentsReload((n) => n + 1), []);

  return {
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
    pendingApprovals,
    waitingApproval,
    terminalApproval,
    resumeBlocked,
    resumeBlockedLabel,
    streaming,
    delegateStatus: streamDelegateStatus,
    manualResumeWaiting,
    cancelWaitingApproval,
    resumeTerminal,
    streamFailure,
    clearStreamFailure,
    cancelStream,
    contentSwitching,
  };
};
