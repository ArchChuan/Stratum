import { describe, expect, it } from 'vitest';

import http, {
  getTokenRef as getSharedTokenRef,
  setupApiInterceptors as setupSharedApiInterceptors,
} from '../http';

import api, { getTokenRef, setupApiInterceptors } from '@/services/client';

describe('shared http compatibility export', () => {
  it('re-exports the canonical api client and auth helpers', () => {
    expect(http).toBe(api);
    expect(getSharedTokenRef).toBe(getTokenRef);
    expect(setupSharedApiInterceptors).toBe(setupApiInterceptors);
  });
});
