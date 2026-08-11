import { beforeEach, describe, expect, it, vi } from 'vitest';

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

describe('executeAgentStream', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.streamApiEvents.mockReturnValue(new AbortController());
  });

  it('preserves the public code from a terminal SSE error', () => {
    const onError = vi.fn();
    executeAgentStream('system', { query: 'hello', context: {}, variables: {} }, {
      onToken: vi.fn(),
      onDone: vi.fn(),
      onError,
      onApprovalRequired: vi.fn(),
    });
    const callbacks = mocks.streamApiEvents.mock.calls[0][2] as {
      onEvent: (event: unknown) => boolean;
    };

    callbacks.onEvent({
      error: '租户尚未配置平台助手模型',
      code: 'SYSTEM_ASSISTANT_MODEL_UNAVAILABLE',
    });

    expect(onError).toHaveBeenCalledOnce();
    const error = onError.mock.calls[0][0] as Error & { code?: string };
    expect(error.message).toBe('租户尚未配置平台助手模型');
    expect(error.code).toBe('SYSTEM_ASSISTANT_MODEL_UNAVAILABLE');
  });
});
