import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ExecuteAgentPayload, StreamCallbacks } from '../../model/agent';
import { ChatStreamProvider, useChatStream } from '../ChatStreamContext';

const mocks = vi.hoisted(() => ({
  executeAgentStream: vi.fn(),
  stopAgentExecution: vi.fn(),
  messageError: vi.fn(),
  messageWarning: vi.fn(),
}));

// mock 路径相对本测试文件解析（不是相对 SUT）：SUT 里写的是 '../api/agent.api'，
// 本文件在 hooks/__tests__/ 下，必须写成 '../../api/agent.api'，否则 mock 不生效、
// executeAgentStream 会真发网络请求。
vi.mock('../../api/agent.api', () => ({
  agentApi: { stopAgentExecution: mocks.stopAgentExecution },
  executeAgentStream: mocks.executeAgentStream,
}));

vi.mock('antd', () => ({
  message: { error: mocks.messageError, success: vi.fn(), warning: mocks.messageWarning },
}));

const payload: ExecuteAgentPayload = { query: '你好', context: {}, variables: {} };

const renderChatStream = () => renderHook(() => useChatStream(), { wrapper: ChatStreamProvider });

const getCaptured = (): StreamCallbacks => {
  if (!captured) throw new Error('executeAgentStream 未被调用，回调未捕获');
  return captured;
};

let captured: StreamCallbacks | null = null;

// 起流并让首帧恢复键落位。onExecutionId 只能在 executeAgentStream 返回之后触发：
// SUT 的守卫是 ctrl 引用相等（ChatStreamContext.tsx:138），而 s.ctrl = ctrl 发生在
// 调用返回之后（:198）。若在 mock 内部同步回调 onExecutionId，那一刻 ctrl 仍为旧值，
// 守卫早退 → executionId 恒为空 → cancelStream 静默不发 stop。
const startAndCaptureExecutionId = (
  stream: { startStream: (agentId: string, payload: ExecuteAgentPayload) => void },
  executionId: string,
) => {
  act(() => stream.startStream('agent-1', payload));
  act(() => getCaptured().onExecutionId?.(executionId));
};

describe('ChatStreamContext cancelStream', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    captured = null;
    mocks.executeAgentStream.mockImplementation(
      (_agentId: string, _payload: ExecuteAgentPayload, callbacks: StreamCallbacks) => {
        captured = callbacks;
        return new AbortController();
      },
    );
    mocks.stopAgentExecution.mockResolvedValue(undefined);
  });

  it('停止时把 stop 请求发给服务端一次，并立刻收口本地流状态', () => {
    const { result } = renderChatStream();
    startAndCaptureExecutionId(result.current, 'exec-1');
    expect(result.current.getStreamState().executionId).toBe('exec-1');
    expect(result.current.streaming).toBe(true);

    act(() => result.current.cancelStream());

    expect(mocks.stopAgentExecution).toHaveBeenCalledTimes(1);
    expect(mocks.stopAgentExecution).toHaveBeenCalledWith('agent-1', 'exec-1');
    expect(result.current.streaming).toBe(false);
    expect(result.current.streamDone).toBe(true);

    // 连点：ctrl 已被首击置空，后续点击早退，不得重复向服务端发 stop。
    act(() => result.current.cancelStream());
    expect(mocks.stopAgentExecution).toHaveBeenCalledTimes(1);
  });

  it('服务端 stop 失败时不回滚本地状态，只提示', async () => {
    mocks.stopAgentExecution.mockRejectedValue(new Error('network down'));
    const { result } = renderChatStream();
    startAndCaptureExecutionId(result.current, 'exec-1');

    await act(async () => {
      result.current.cancelStream();
    });

    expect(mocks.stopAgentExecution).toHaveBeenCalledTimes(1);
    // 承重不变量：用户已看到停止生效，失败只提示、不回退本地状态——不得把 UI 弹回流式中。
    expect(result.current.streaming).toBe(false);
    expect(result.current.streamDone).toBe(true);
    // 失败必须暴露，不得吞没；同时锁定仓库统一通知形态（content + duration），
    // 只断言调用次数会让被破坏的通知形状静默通过。
    expect(mocks.messageError).toHaveBeenCalledTimes(1);
    expect(mocks.messageError).toHaveBeenCalledWith({ content: '停止失败', duration: 3 });
  });

  it('续跑路径首帧到达前点停止也能发出 stop（凭 payload.execution_id 回填）', () => {
    const { result } = renderChatStream();
    // 续跑(doApprovalResume/doFreshResume)显式携带 execution_id，无需等 meta 首帧。
    act(() => result.current.startStream('agent-1', { ...payload, execution_id: 'exec-resume' }));
    expect(result.current.getStreamState().executionId).toBe('exec-resume');

    act(() => result.current.cancelStream());

    expect(mocks.stopAgentExecution).toHaveBeenCalledWith('agent-1', 'exec-resume');
    expect(mocks.messageWarning).not.toHaveBeenCalled();
  });

  it('全新执行首帧到达前点停止：stop 无法投递时必须可见告警，不得静默', () => {
    const { result } = renderChatStream();
    // 全新执行 payload 无 execution_id：撤销本地流，但恢复键尚未由首帧下发。
    act(() => result.current.startStream('agent-1', payload));
    expect(result.current.getStreamState().executionId).toBeNull();

    act(() => result.current.cancelStream());

    expect(mocks.stopAgentExecution).not.toHaveBeenCalled();
    expect(mocks.messageWarning).toHaveBeenCalledWith({
      content: '停止请求未发送：执行尚未建立，请稍后重试',
      duration: 3,
    });
  });
});
