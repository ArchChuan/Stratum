import { test } from '@playwright/test';

import { parseRuntimeOptions } from './stateful/core/runtime';

const runtime = parseRuntimeOptions(process.env);

for (const pack of runtime.packs) {
  test(`${pack} stateful acceptance`, async ({ browserName }) => {
    test.skip(true, `pack ${pack} is registered by its domain implementation`);
    if (browserName !== 'chromium') throw new Error('system stateful acceptance requires Chromium');
  });
}
