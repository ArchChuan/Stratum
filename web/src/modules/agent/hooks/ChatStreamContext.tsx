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
	approval: ToolApproval | null;
	// 断线续接恢复键:SSE 首帧下发,存内存仅暴露,断线重发由 executeAgentStream
	// 内部携带,这里只做可读快照(刷新降级为重新执行,不持久化)。
	executionId: string | null;
	// 委托子 agent 进度(SSE delegate_status):running 时展示 banner,finished 清空;
	// 非持久化,断线重连由 execution_id 恢复后服务端重新下发。
	delegateStatus: DelegateStatusView;
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
	approval: ToolApproval | null;
	executionId: string | null;
	delegateStatus: DelegateStatusView;
}

interface ChatStreamContextValue {
  streaming: boolean;
  streamConversationId: string | null;
  accumulatedContent: string;
  streamResult: AgentExecutionResult | null;
  streamError: string | null;
	streamDone: boolean;
	streamApproval: ToolApproval | null;
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
		approval: null,
		executionId: null,
		delegateStatus: null,
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
		s.approval = null;
		s.executionId = null;
		s.delegateStatus = null;
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
		stateRef.current.error = err.message || String(err);
		stateRef.current.failure = {
			message: err.message || String(err),
			code: (err as Error & { code?: string }).code,
			status: (err as Error & { status?: number }).status,
		};
        stateRef.current.ctrl = null;
        notify();
		},
		onApprovalRequired: (approval) => {
			if (stateRef.current.ctrl !== ctrl) return;
			stateRef.current.done = true; stateRef.current.delegateStatus = null; stateRef.current.approval = approval; stateRef.current.ctrl = null; notify();
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
		approval: stateRef.current.approval,
		executionId: stateRef.current.executionId,
		delegateStatus: stateRef.current.delegateStatus,
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
      streamApproval: stateRef.current.approval,
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
