import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useResponsive } from '../useResponsive';

type ChangeListener = (event: MediaQueryListEvent) => void;

class MatchMediaController {
  private width = 1200;
  readonly queries = new Map<string, Set<ChangeListener>>();

  matchMedia = vi.fn((query: string): MediaQueryList => {
    const listeners = this.queries.get(query) ?? new Set<ChangeListener>();
    this.queries.set(query, listeners);
    const addEventListener = ((_type: string, listener: EventListenerOrEventListenerObject) => {
      if (typeof listener === 'function') listeners.add(listener as ChangeListener);
    }) as MediaQueryList['addEventListener'];
    const removeEventListener = ((_type: string, listener: EventListenerOrEventListenerObject) => {
      if (typeof listener === 'function') listeners.delete(listener as ChangeListener);
    }) as MediaQueryList['removeEventListener'];

    return {
      get matches() {
        const maxWidth = Number(query.match(/max-width:\s*(\d+)px/)?.[1]);
        return Number.isFinite(maxWidth) && controller.width <= maxWidth;
      },
      media: query,
      onchange: null,
      addEventListener,
      removeEventListener,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    };
  });

  setWidth(width: number) {
    this.width = width;
    this.queries.forEach((listeners, query) => {
      const matches = width <= Number(query.match(/max-width:\s*(\d+)px/)?.[1]);
      listeners.forEach((listener) => listener({ matches, media: query } as MediaQueryListEvent));
    });
  }
}

let controller: MatchMediaController;

describe('useResponsive', () => {
  beforeEach(() => {
    controller = new MatchMediaController();
    vi.stubGlobal('matchMedia', controller.matchMedia);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('identifies mobile below 768px and compact below 1024px', () => {
    controller.setWidth(767);
    const { result } = renderHook(() => useResponsive());

    expect(result.current).toEqual({ isMobile: true, isCompact: true });
  });

  it('identifies compact tablet widths without marking them mobile', () => {
    controller.setWidth(768);
    const { result } = renderHook(() => useResponsive());

    expect(result.current).toEqual({ isMobile: false, isCompact: true });
  });

  it('updates on media query changes and removes listeners on unmount', () => {
    const { result, unmount } = renderHook(() => useResponsive());

    expect(result.current).toEqual({ isMobile: false, isCompact: false });

    act(() => controller.setWidth(390));
    expect(result.current).toEqual({ isMobile: true, isCompact: true });

    unmount();
    expect([...controller.queries.values()].every((listeners) => listeners.size === 0)).toBe(true);
  });
});
