import { describe, expect, it, vi } from 'vitest';

import api, {
  getTokenRef,
  markAuthReady,
  resetAuthReady,
  setupApiInterceptors,
  streamApiGet,
  streamApiEvents,
} from './client';

describe('api client', () => {
  it('does not wait for auth readiness before guest login', async () => {
    resetAuthReady();
    setupApiInterceptors({ current: null });
    const requestInterceptors = api.interceptors.request as unknown as {
      handlers: Array<{
        fulfilled?: (config: { headers: Record<string, unknown>; url: string }) => Promise<unknown>;
      }>;
    };
    const latestInterceptor = requestInterceptors.handlers[requestInterceptors.handlers.length - 1];
    let settled = false;

    void latestInterceptor?.fulfilled?.({ headers: {}, url: '/auth/guest' }).then(() => {
      settled = true;
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(settled).toBe(true);
    markAuthReady();
  });

  it('waits for auth and attaches the memory token for the tenant model catalogue', async () => {
    resetAuthReady();
    const tokenRef = { current: 'model-catalogue-token' };
    setupApiInterceptors(tokenRef);
    const requestInterceptors = api.interceptors.request as unknown as {
      handlers: Array<{
        fulfilled?: (config: { headers: Record<string, unknown>; url: string }) => Promise<unknown>;
      }>;
    };
    const latestInterceptor = requestInterceptors.handlers[requestInterceptors.handlers.length - 1];
    let resolved: { headers?: Record<string, unknown> } | undefined;
    const pending = latestInterceptor?.fulfilled?.({ headers: {}, url: '/models' }).then((config) => {
      resolved = config as { headers?: Record<string, unknown> };
    });
    await Promise.resolve();
    expect(resolved).toBeUndefined();

    markAuthReady();
    await pending;
    expect(resolved?.headers).toMatchObject({ Authorization: 'Bearer model-catalogue-token' });
  });

  it('does not read or write access tokens from localStorage', async () => {
    const getItem = vi.spyOn(Storage.prototype, 'getItem');
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    const removeItem = vi.spyOn(Storage.prototype, 'removeItem');
    const tokenRef = { current: 'memory-token' };

    markAuthReady();
    setupApiInterceptors(tokenRef);
    const requestInterceptors = api.interceptors.request as unknown as {
      handlers: Array<{
        fulfilled?: (config: { headers: Record<string, unknown>; url: string }) => unknown;
      }>;
    };
    const latestInterceptor = requestInterceptors.handlers[requestInterceptors.handlers.length - 1];
    const config = await latestInterceptor?.fulfilled?.({
      headers: {},
      url: '/agents',
    }) as { headers?: Record<string, unknown> } | undefined;

    expect(config?.headers).toMatchObject({ Authorization: 'Bearer memory-token' });
    expect(getItem).not.toHaveBeenCalled();
    expect(setItem).not.toHaveBeenCalled();
    expect(removeItem).not.toHaveBeenCalled();
  });

  it('streams SSE requests with memory token auth and cookie credentials', async () => {
    getTokenRef().current = 'stream-token';
    const encoder = new TextEncoder();
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode('data: {"token":"hi"}\n\n'));
        controller.close();
      },
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream));
    vi.stubGlobal('fetch', fetchMock);
    const onEvent = vi.fn();
    const onError = vi.fn();

    streamApiEvents('/agents/a1/execute/stream', { query: 'hello' }, { onEvent, onError });
    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledWith({ token: 'hi' }));

    expect(fetchMock).toHaveBeenCalledWith(
      '/agents/a1/execute/stream',
      expect.objectContaining({
        credentials: 'include',
        headers: expect.objectContaining({
          Authorization: 'Bearer stream-token',
          'Content-Type': 'application/json',
        }),
        method: 'POST',
      }),
    );
    expect(onError).not.toHaveBeenCalled();
  });

  it('parses data from named SSE events', async () => {
    const encoder = new TextEncoder();
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode(
          'event: approval_required\ndata: {"status":"waiting_approval","approvalId":"approval-1"}\n\n',
        ));
        controller.close();
      },
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)));
    const onEvent = vi.fn().mockReturnValue(false);
    const onClose = vi.fn();
    const onError = vi.fn();

    streamApiEvents('/agents/a1/execute/stream', { query: 'delete' }, {
      onEvent,
      onClose,
      onError,
    });

    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledWith({
      status: 'waiting_approval',
      approvalId: 'approval-1',
    }));
    expect(onClose).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
  });

  it('preserves status and public code from a failed stream request', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: '该 Agent 尚未配置可用模型',
      code: 'ASSISTANT_MODEL_UNAVAILABLE',
    }), {
      status: 503,
      headers: { 'Content-Type': 'application/json' },
    })));
    const onError = vi.fn();

    streamApiEvents('/agents/system/execute/stream', { query: 'hello' }, {
      onEvent: vi.fn(),
      onError,
    });

    await vi.waitFor(() => expect(onError).toHaveBeenCalledOnce());
    const error = onError.mock.calls[0][0] as Error & { status?: number; code?: string };
    expect(error.message).toBe('该 Agent 尚未配置可用模型');
    expect(error.status).toBe(503);
    expect(error.code).toBe('ASSISTANT_MODEL_UNAVAILABLE');
  });

  it('streams resumable GET SSE with shared auth, cookies, and Last-Event-ID', async () => {
    getTokenRef().current = 'run-token';
    const encoder = new TextEncoder();
    const stream = new ReadableStream({ start(controller) { controller.enqueue(encoder.encode('id: 9\nevent: workflow.node_completed\ndata: {"sequence_no":9}\n\n')); controller.close(); } });
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream));
    vi.stubGlobal('fetch', fetchMock);
    const onEvent = vi.fn();
    streamApiGet('/workflow-runs/run-1/events/stream', { lastEventId: '8', onEvent, onError: vi.fn() });
    await vi.waitFor(() => expect(onEvent).toHaveBeenCalledWith({ id: '9', event: 'workflow.node_completed', data: { sequence_no: 9 } }));
    expect(fetchMock).toHaveBeenCalledWith('/workflow-runs/run-1/events/stream', expect.objectContaining({ method: 'GET', credentials: 'include', headers: expect.objectContaining({ Authorization: 'Bearer run-token', 'Last-Event-ID': '8' }) }));
  });
});
