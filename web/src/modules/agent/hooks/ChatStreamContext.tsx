import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';

import { executeAgentStream } from '../api/agent.api';
import type {
  AgentExecutionFailure,
  AgentExecutionResult,
  DelegateStatusView,
  ExecuteAgentPayload,
  ToolApproval,
} from '../model/agent';

interface StreamInternalState {
  conversationId: string | null;
  agentId: string | null;
  userQuery: string | null;
  content: string;
  done: boolean;
  result: AgentExecutionResult | null;
	error: string | null;
	failure: AgentExecutionFailure | null;
	// 多审批：同一轮 LLM 消息含多个需审批工具时，SSE 一帧带全部审批（批量渲染卡）。
	approvals: ToolApproval[];
	// 断线续接恢复键:SSE 首帧下发,存内存仅暴露,断线重发由 executeAgentStream
	// 内部携带,这里只做可读快照(刷新降级为重新执行,不持久化)。
	executionId: string | null;
	// 委托子 agent 进度(SSE delegate_status):running 时展示 banner,finished 清空;
	// 非持久化,断线重连由 execution_id 恢复后服务端重新下发。
	delegateStatus: DelegateStatusView;
		conflict: boolean;
  ctrl: AbortController | null;
}

export interface StreamSnapshot {
  streaming: boolean;
  conversationId: string | null;
  userQuery: string | null;
  content: string;
  done: boolean;
  result: AgentExecutionResult | null;
	error: string | null;
	approvals: ToolApproval[];
	executionId: string | null;
	delegateStatus: DelegateStatusView;
	conflict: boolean;
}

interface ChatStreamContextValue {
  streaming: boolean;
  streamConversationId: string | null;
  accumulatedContent: string;
  streamResult: AgentExecutionResult | null;
  streamError: string | null;
	streamDone: boolean;
	streamApprovals: ToolApproval[];
	streamConflict: boolean;
	streamFailure: AgentExecutionFailure | null;
	streamDelegateStatus: DelegateStatusView;
  startStream: (agentId: string, payload: ExecuteAgentPayload) => void;
  cancelStream: () => void;
	clearStreamFailure: () => void;
  getStreamState: () => StreamSnapshot;
}

const ChatStreamContext = createContext<ChatStreamContextValue | null>(null);

export const useChatStream = (): ChatStreamContextValue => {
  const ctx = useContext(ChatStreamContext);
  if (!ctx) throw new Error('useChatStream must be used within ChatStreamProvider');
  return ctx;
};

export const ChatStreamProvider = ({ children }: { children: ReactNode }) => {
  const [tick, setTick] = useState(0);
  const stateRef = useRef<StreamInternalState>({
    conversationId: null,
    agentId: null,
    userQuery: null,
    content: '',
    done: false,
    result: null,
		error: null,
		failure: null,
		approvals: [],
		executionId: null,
		delegateStatus: null,
		conflict: false,
    ctrl: null,
  });

  const rafRef = useRef<number | null>(null);
  const notify = useCallback(() => setTick((t) => t + 1), []);

  // Batch token updates via RAF to avoid one re-render per token.
  const scheduleNotify = useCallback(() => {
    if (rafRef.current !== null) return;
    rafRef.current = requestAnimationFrame(() => {
      rafRef.current = null;
      setTick((t) => t + 1);
    });
  }, []);

  // Cancel pending RAF on unmount.
  useEffect(() => {
    return () => {
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
    };
  }, []);

  const startStream = useCallback((agentId: string, payload: ExecuteAgentPayload) => {
    const s = stateRef.current;
    if (s.ctrl) s.ctrl.abort();
    s.conversationId = payload.conversation_id ?? null;
    s.agentId = agentId;
    s.userQuery = payload.query || null;
    s.content = '';
    s.done = false;
    s.result = null;
		s.error = null;
		s.failure = null;
		s.approvals = [];
		s.executionId = null;
		s.delegateStatus = null;
		s.conflict = false;
    notify();

    const ctrl = executeAgentStream(agentId, payload, {
      onExecutionId: (executionId) => {
        if (stateRef.current.ctrl !== ctrl) return;
        stateRef.current.executionId = executionId;
      },
      onToken: (token) => {
        if (stateRef.current.ctrl !== ctrl) return;
        stateRef.current.content += token;
        scheduleNotify(); // batched: ~1 render per animation frame (~60fps)
      },
      onDone: (data) => {
        if (stateRef.current.ctrl !== ctrl) return;
        stateRef.current.done = true;
        stateRef.current.delegateStatus = null; // 终态清理:断连/错误时不残留 banner
        stateRef.current.result = data;
        stateRef.current.ctrl = null;
        notify(); // immediate: stream is over
      },
		onError: (err) => {
        if (stateRef.current.ctrl !== ctrl) return;
        stateRef.current.done = true;
		stateRef.current.delegateStatus = null;
		// F3 双 tab 并发续跑败者：另一窗口已抢占并流式（后端 claimApprovalResume
		// CAS 失败 → 409）。静默等待胜方结果，不污染对话气泡、不上抛错误；审批
		// 卡片终态由 active-execution 轮询刷新。
	if ((err as Error & { status?: number }).status === 409) {
		stateRef.current.error = null;
		stateRef.current.failure = null;
		stateRef.current.ctrl = null;
		// 双 tab 并发续跑败者标记:useChatPage 据此保留占位消息并交轮询做败方同步。
		stateRef.current.conflict = true;
		notify();
		return;
	}
		stateRef.current.error = err.message || String(err);
		stateRef.current.failure = {
			message: err.message || String(err),
			code: (err as Error & { code?: string }).code,
			status: (err as Error & { status?: number }).status,
		};
        stateRef.current.ctrl = null;
        notify();
		},
		onApprovalsRequired: (approvals) => {
			if (stateRef.current.ctrl !== ctrl) return;
			stateRef.current.done = true; stateRef.current.delegateStatus = null; stateRef.current.approvals = approvals; stateRef.current.ctrl = null; notify();
		},
			onDelegateEvent: (evt) => {
				if (stateRef.current.ctrl !== ctrl) return;
				const convId = stateRef.current.conversationId;
				// finished 且非 failed 时清空：running→finished 仅反映子循环结束，
				// 最终回答由后续轮次自然呈现；failed 保留失败 Tag 直至终态清理。
				if (evt.delegate_status === 'running') {
					stateRef.current.delegateStatus = { status: 'running', goal: evt.goal, delegateId: evt.delegate_id, conversationId: convId };
				} else if (evt.delegate_status === 'finished' && evt.result_status === 'failed') {
					stateRef.current.delegateStatus = { status: 'finished', resultStatus: 'failed', goal: evt.goal, delegateId: evt.delegate_id, summary: evt.summary, conversationId: convId };
				} else {
					stateRef.current.delegateStatus = null;
				}
				notify();
			},
    });
    s.ctrl = ctrl;
  }, [notify, scheduleNotify]);

  const cancelStream = useCallback(() => {
    const s = stateRef.current;
    if (s.ctrl) {
      s.ctrl.abort();
      s.ctrl = null;
      s.delegateStatus = null;
      s.done = true;
      notify();
    }
  }, [notify]);

	const clearStreamFailure = useCallback(() => {
		if (!stateRef.current.failure) return;
		stateRef.current.failure = null;
		notify();
	}, [notify]);

  const getStreamState = useCallback(
    (): StreamSnapshot => ({
      streaming: !stateRef.current.done && stateRef.current.ctrl !== null,
      conversationId: stateRef.current.conversationId,
      userQuery: stateRef.current.userQuery,
      content: stateRef.current.content,
      done: stateRef.current.done,
      result: stateRef.current.result,
		error: stateRef.current.error,
		approvals: stateRef.current.approvals,
		executionId: stateRef.current.executionId,
		delegateStatus: stateRef.current.delegateStatus,
		conflict: stateRef.current.conflict,
    }),
    [],
  );

  // 流状态全部存于 stateRef，tick 是唯一变更信号；useMemo 保证未变更时
  // value 引用稳定，避免流式期间全应用消费子树每帧重渲染。
  const value: ChatStreamContextValue = useMemo(
    () => ({
      streaming: !stateRef.current.done && stateRef.current.ctrl !== null,
      streamConversationId: stateRef.current.conversationId,
      accumulatedContent: stateRef.current.content,
      streamResult: stateRef.current.result,
      streamError: stateRef.current.error,
      streamDone: stateRef.current.done,
      streamApprovals: stateRef.current.approvals,
      streamConflict: stateRef.current.conflict,
      streamFailure: stateRef.current.failure,
      streamDelegateStatus: stateRef.current.delegateStatus,
      startStream,
      cancelStream,
      clearStreamFailure,
      getStreamState,
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- tick 是函数体外的强制重算信号（stateRef 读取不触发重渲染），刻意放在依赖数组
    [tick, startStream, cancelStream, clearStreamFailure, getStreamState],
  );

  return <ChatStreamContext.Provider value={value}>{children}</ChatStreamContext.Provider>;
};
