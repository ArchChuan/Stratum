import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';

import { executeAgentStream } from '../api/agent.api';
import type { AgentExecutionResult, ExecuteAgentPayload } from '../model/agent';

interface StreamInternalState {
  conversationId: string | null;
  agentId: string | null;
  userQuery: string | null;
  content: string;
  done: boolean;
  result: AgentExecutionResult | null;
  error: string | null;
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
}

interface ChatStreamContextValue {
  streaming: boolean;
  streamConversationId: string | null;
  accumulatedContent: string;
  streamResult: AgentExecutionResult | null;
  streamError: string | null;
  streamDone: boolean;
  startStream: (agentId: string, payload: ExecuteAgentPayload) => void;
  cancelStream: () => void;
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
    ctrl: null,
  });

  const rafRef = useRef<number | null>(null);

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

  const notify = useCallback(() => setTick((t) => t + 1), []);

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
    notify();

    const ctrl = executeAgentStream(agentId, payload, {
      onToken: (token) => {
        if (stateRef.current.ctrl !== ctrl) return;
        stateRef.current.content += token;
        scheduleNotify(); // batched: ~1 render per animation frame (~60fps)
      },
      onDone: (data) => {
        if (stateRef.current.ctrl !== ctrl) return;
        stateRef.current.done = true;
        stateRef.current.result = data;
        stateRef.current.ctrl = null;
        notify(); // immediate: stream is over
      },
      onError: (err) => {
        if (stateRef.current.ctrl !== ctrl) return;
        stateRef.current.done = true;
        stateRef.current.error = err.message || String(err);
        stateRef.current.ctrl = null;
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
      s.done = true;
      notify();
    }
  }, []);

  const getStreamState = useCallback(
    (): StreamSnapshot => ({
      streaming: !stateRef.current.done && stateRef.current.ctrl !== null,
      conversationId: stateRef.current.conversationId,
      userQuery: stateRef.current.userQuery,
      content: stateRef.current.content,
      done: stateRef.current.done,
      result: stateRef.current.result,
      error: stateRef.current.error,
    }),
    [],
  );

  const value: ChatStreamContextValue = {
    streaming: !stateRef.current.done && stateRef.current.ctrl !== null,
    streamConversationId: stateRef.current.conversationId,
    accumulatedContent: stateRef.current.content,
    streamResult: stateRef.current.result,
    streamError: stateRef.current.error,
    streamDone: stateRef.current.done,
    startStream,
    cancelStream,
    getStreamState,
  };

  void tick;

  return <ChatStreamContext.Provider value={value}>{children}</ChatStreamContext.Provider>;
};
