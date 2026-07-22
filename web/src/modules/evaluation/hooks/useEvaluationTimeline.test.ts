import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useEvaluationTimeline } from './useEvaluationTimeline';

const api = vi.hoisted(() => ({ getTimeline: vi.fn() }));
vi.mock('../api/evaluation.api', () => ({ evaluationApi: api }));

const deferred = <T,>() => { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((ok, fail) => { resolve = ok; reject = fail; }); return { promise, resolve, reject }; };
const resource = (id: string) => ({ resource_kind: 'skill' as const, resource_id: id });

describe('useEvaluationTimeline', () => {
  beforeEach(() => api.getTimeline.mockReset());

  it('clears A events when opening B and B fails', async () => {
    api.getTimeline.mockResolvedValueOnce({ items: [{ id: 'a', summary: 'A 审计' }] })
      .mockRejectedValueOnce(new Error('B 失败'));
    const { result } = renderHook(() => useEvaluationTimeline());
    await act(async () => result.current.openTimeline(resource('a')));
    expect(result.current.events[0].summary).toBe('A 审计');
    await act(async () => result.current.openTimeline(resource('b')));
    expect(result.current.events).toEqual([]);
    expect(result.current.error).toBe('加载资源时间线失败');
  });

  it('ignores out-of-order A after B becomes current and clears on close', async () => {
    const a = deferred<any>(); const b = deferred<any>();
    api.getTimeline.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
    const { result } = renderHook(() => useEvaluationTimeline());
    act(() => { void result.current.openTimeline(resource('a')); void result.current.openTimeline(resource('b')); });
    await act(async () => b.resolve({ items: [{ id: 'b', summary: 'B 审计' }] }));
    await act(async () => a.resolve({ items: [{ id: 'a', summary: 'A 审计' }] }));
    expect(result.current.events[0].summary).toBe('B 审计');
    act(() => result.current.closeTimeline());
    expect(result.current.events).toEqual([]);
  });
});
