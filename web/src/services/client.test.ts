import { describe, expect, it, vi } from 'vitest';

import api, {
  getTokenRef,
  markAuthReady,
  resetAuthReady,
  setupApiInterceptors,
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
});
