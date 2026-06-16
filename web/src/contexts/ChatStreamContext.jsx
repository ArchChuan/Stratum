import React, { createContext, useContext, useRef, useState, useCallback } from 'react';
import { executeAgentStream } from '../services/api';

const ChatStreamContext = createContext(null);

export const useChatStream = () => useContext(ChatStreamContext);

export const ChatStreamProvider = ({ children }) => {
  const [tick, setTick] = useState(0);
  const stateRef = useRef({
    conversationId: null,
    agentId: null,
    userQuery: null,
    content: '',
    done: false,
    result: null,
    error: null,
    ctrl: null,
  });

  const notify = () => setTick(t => t + 1);

  const startStream = useCallback((agentId, payload) => {
    const s = stateRef.current;
    if (s.ctrl) {
      s.ctrl.abort();
    }
    s.conversationId = payload.conversation_id;
    s.agentId = agentId;
    s.userQuery = payload.query || null;
    s.content = '';
    s.done = false;
    s.result = null;
    s.error = null;
    notify();

    const ctrl = executeAgentStream(agentId, payload, {
      onToken: (token) => {
        stateRef.current.content += token;
        notify();
      },
      onDone: (data) => {
        stateRef.current.done = true;
        stateRef.current.result = data;
        stateRef.current.ctrl = null;
        notify();
      },
      onError: (err) => {
        stateRef.current.done = true;
        stateRef.current.error = err.message || String(err);
        stateRef.current.ctrl = null;
        notify();
      },
    });
    s.ctrl = ctrl;
  }, []);

  const cancelStream = useCallback(() => {
    const s = stateRef.current;
    if (s.ctrl) {
      s.ctrl.abort();
      s.ctrl = null;
      s.done = true;
      notify();
    }
  }, []);

  const getStreamState = useCallback(() => ({
    streaming: !stateRef.current.done && stateRef.current.ctrl !== null,
    conversationId: stateRef.current.conversationId,
    userQuery: stateRef.current.userQuery,
    content: stateRef.current.content,
    done: stateRef.current.done,
    result: stateRef.current.result,
    error: stateRef.current.error,
  }), []);

  const value = {
    streaming: !stateRef.current.done && stateRef.current.ctrl !== null,
    streamConversationId: stateRef.current.conversationId,
    accumulatedContent: stateRef.current.content,
    streamResult: stateRef.current.result,
    streamError: stateRef.current.error,
    streamDone: stateRef.current.done,
    startStream,
    cancelStream,
    getStreamState,
    _tick: tick,
  };

  return (
    <ChatStreamContext.Provider value={value}>
      {children}
    </ChatStreamContext.Provider>
  );
};
