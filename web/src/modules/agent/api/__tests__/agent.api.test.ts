import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  streamApiEvents: vi.fn(),
}));

vi.mock('@/services/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/services/client')>();
  return {
    ...actual,
    streamApiEvents: mocks.streamApiEvents,
  };
});

import { executeAgentStream } from '../agent.api';

import {
  AGENT_STREAM_RECONNECT_BASE_MS,
  AGENT_STREAM_RECONNECT_MAX_ATTEMPTS,
  AGENT_STREAM_RECONNECT_MAX_MS,
} from '@/constants';
import { StreamRequestError } from '@/services/client';

const callbacksOf = (index: number) =>
  mocks.streamApiEvents.mock.calls[index][2] as {
    onEvent: (event: Record<string, unknown>) => boolean;
    onClose: () => void;
    onError: (err: Error) => void;
  };

const payloadOf = (index: number) =>
  mocks.streamApiEvents.mock.calls[index][1] as { query?: string; conversation_id?: string; execution_id?: string };

describe('executeAgentStream', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useRealTimers();
    mocks.streamApiEvents.mockReturnValue(new AbortController());
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('preserves the public code from a terminal SSE error', () => {
    const onError = vi.fn();
    executeAgentStream('system', { query: 'hello', context: {}, variables: {} }, {
      onToken: vi.fn(),
      onDone: vi.fn(),
      onError,
      onApprovalsRequired: vi.fn(),
    });

    callbacksOf(0).onEvent({
      error: '该 Agent 尚未配置可用模型',
      code: 'ASSISTANT_MODEL_UNAVAILABLE',
    });

    expect(onError).toHaveBeenCalledOnce();
    const error = onError.mock.calls[0][0] as Error & { code?: string };
    expect(error.message).toBe('该 Agent 尚未配置可用模型');
    expect(error.code).toBe('ASSISTANT_MODEL_UNAVAILABLE');
  });

  describe('断点续接自愈连接器', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    it('首帧捕获 execution_id 并回调,不影响后续 token', () => {
      const onExecutionId = vi.fn();
      const onToken = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken,
        onDone: vi.fn(),
        onError: vi.fn(),
        onApprovalsRequired: vi.fn(),
        onExecutionId,
      });

      expect(callbacksOf(0).onEvent({ execution_id: 'exec-1' })).toBe(true);
      expect(onExecutionId).toHaveBeenCalledWith('exec-1');
      expect(onToken).not.toHaveBeenCalled(); // 首帧不是 token 帧

      callbacksOf(0).onEvent({ token: '你好' });
      expect(onToken).toHaveBeenCalledWith('你好');
    });

    it('断线(非 done 关闭)退避重连,重发请求携带同一 execution_id', () => {
      const onToken = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', conversation_id: 'conv-1', context: {}, variables: {} }, {
        onToken,
        onDone: vi.fn(),
        onError: vi.fn(),
        onApprovalsRequired: vi.fn(),
      });

      callbacksOf(0).onEvent({ execution_id: 'exec-1' });
      callbacksOf(0).onEvent({ token: '你好' });
      expect(onToken).toHaveBeenCalledWith('你好');

      // 半路断流:onClose(未到 done 帧)→ 退避重连
      callbacksOf(0).onClose();
      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_BASE_MS);

      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(2);
      expect(payloadOf(1).execution_id).toBe('exec-1');
      expect(payloadOf(1).query).toBe('hi');
      expect(payloadOf(1).conversation_id).toBe('conv-1');
    });

    it('done 帧后不再重连', () => {
      const onDone = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone,
        onError: vi.fn(),
        onApprovalsRequired: vi.fn(),
      });

      callbacksOf(0).onEvent({ done: true, output: 'ok' });
      expect(onDone).toHaveBeenCalledOnce();

      callbacksOf(0).onClose();
      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_MAX_MS * 2);
      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(1);
    });

    it('error 帧报告错误后不再重连', () => {
      const onError = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone: vi.fn(),
        onError,
        onApprovalsRequired: vi.fn(),
      });

      callbacksOf(0).onEvent({ error: 'boom', code: 'X' });
      expect(onError).toHaveBeenCalledOnce();

      callbacksOf(0).onClose();
      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_MAX_MS * 2);
      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(1);
    });

    it('approval 帧触发回调后不再重连', () => {
      const onApproval = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone: vi.fn(),
        onError: vi.fn(),
        onApprovalsRequired: onApproval,
      });

      callbacksOf(0).onEvent({ status: 'waiting_approval', approvalId: 'a-1', toolName: 't', riskLevel: 'high' });
      expect(onApproval).toHaveBeenCalledOnce();
      // 无 approvals 数组时回退顶层镜像单条;回调参数恒为数组。
      expect(onApproval.mock.calls[0][0]).toEqual([
        { approvalId: 'a-1', toolName: 't', serverId: '', riskLevel: 'high', status: 'pending' },
      ]);

      callbacksOf(0).onClose();
      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_MAX_MS * 2);
      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(1);
    });

    it('批量审批帧:approvals 数组一次回调全部', () => {
      const onApproval = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone: vi.fn(),
        onError: vi.fn(),
        onApprovalsRequired: onApproval,
      });

      callbacksOf(0).onEvent({
        status: 'waiting_approval',
        approvals: [
          { approvalId: 'a-1', toolName: 'delete', serverId: 'orders', riskLevel: 'destructive' },
          { approvalId: 'a-2', toolName: 'archive', serverId: 'orders', riskLevel: 'destructive' },
        ],
      });
      expect(onApproval).toHaveBeenCalledOnce();
      expect(onApproval.mock.calls[0][0]).toEqual([
        { approvalId: 'a-1', toolName: 'delete', serverId: 'orders', riskLevel: 'destructive', status: 'pending' },
        { approvalId: 'a-2', toolName: 'archive', serverId: 'orders', riskLevel: 'destructive', status: 'pending' },
      ]);

      callbacksOf(0).onClose();
      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_MAX_MS * 2);
      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(1);
    });

    it('4xx 是协议错误,直接终止不重连', () => {
      const onError = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone: vi.fn(),
        onError,
        onApprovalsRequired: vi.fn(),
      });

      callbacksOf(0).onError(new StreamRequestError('HTTP 400', 400));
      expect(onError).toHaveBeenCalledOnce();

      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_MAX_MS * 2);
      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(1);
    });

    it('5xx/网络错误退避重发', () => {
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone: vi.fn(),
        onError: vi.fn(),
        onApprovalsRequired: vi.fn(),
      });

      callbacksOf(0).onError(new StreamRequestError('HTTP 500', 500));
      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_BASE_MS);
      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(2);
    });

    it('用户 cancel 后不再重连', () => {
      const ctrl = executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone: vi.fn(),
        onError: vi.fn(),
        onApprovalsRequired: vi.fn(),
      });

      ctrl.abort();
      callbacksOf(0).onClose();
      vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_MAX_MS * 2);
      expect(mocks.streamApiEvents).toHaveBeenCalledTimes(1);
    });

    it('超过最大重试次数后报错终止', () => {
      const onError = vi.fn();
      executeAgentStream('agent-1', { query: 'hi', context: {}, variables: {} }, {
        onToken: vi.fn(),
        onDone: vi.fn(),
        onError,
        onApprovalsRequired: vi.fn(),
      });

      // 连续断线:每次 onClose 计划一次重连,advance 后 connect 立即发生
      for (let i = 0; i < AGENT_STREAM_RECONNECT_MAX_ATTEMPTS; i += 1) {
        callbacksOf(i).onClose();
        vi.advanceTimersByTime(AGENT_STREAM_RECONNECT_MAX_MS);
      }
      // 第 MAX_ATTEMPTS 次之后仍断线 → attempt 超限,onError 终止
      callbacksOf(AGENT_STREAM_RECONNECT_MAX_ATTEMPTS).onClose();
      expect(onError).toHaveBeenCalledOnce();
      expect(onError.mock.calls[0][0].message).toContain('Stream reconnected');
    });
  });
});
