import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

import { fetchRegisteredRoutes, loadGoldenRoutes } from './routes-io';

describe('fetchRegisteredRoutes', () => {
  it('parses the routes array from a successful response', async () => {
    const routes = [
      { method: 'GET', path: '/memory' },
      { method: 'POST', path: '/memory/clear' },
    ];
    const fetchFn = async () => new Response(JSON.stringify({ routes }), { status: 200 });
    await expect(fetchRegisteredRoutes('http://backend:8080', fetchFn)).resolves.toEqual(routes);
  });

  it('propagates a fetch rejection', async () => {
    const fetchFn = async () => { throw new Error('network down'); };
    await expect(fetchRegisteredRoutes('http://backend:8080', fetchFn))
      .rejects.toThrow('network down');
  });

  it('propagates a non-OK status', async () => {
    const fetchFn = async () => new Response('nope', { status: 503 });
    await expect(fetchRegisteredRoutes('http://backend:8080', fetchFn))
      .rejects.toThrow('GET /e2e/routes failed: 503');
  });
});

describe('loadGoldenRoutes', () => {
  const withTempDir = async (run: (dir: string) => Promise<void>): Promise<void> => {
    const dir = await mkdtemp(join(tmpdir(), 'routes-io-'));
    try {
      await run(dir);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  };

  it('normalizes query paths, dedupes and sorts entries across files', async () => {
    await withTempDir(async (goldenDir) => {
      await writeFile(join(goldenDir, 'a.golden.json'), JSON.stringify([
        { name: 'with-query', method: 'GET', path: '/tenant/members?role=admin,owner' },
        { name: 'duplicated', method: 'POST', path: '/memory/clear' },
      ]));
      await writeFile(join(goldenDir, 'b.golden.json'), JSON.stringify([
        { name: 'same-as-a', method: 'POST', path: '/memory/clear' },
        { name: 'named-param', method: 'GET', path: '/agents/:id/execute' },
      ]));
      const routes = await loadGoldenRoutes(goldenDir);
      // query 剥离、:id 归一化为 :param、重复项去重、按 methodPathKey 排序。
      expect(routes).toEqual([
        { method: 'GET', path: '/agents/:param/execute' },
        { method: 'GET', path: '/tenant/members' },
        { method: 'POST', path: '/memory/clear' },
      ]);
    });
  });

  it('propagates a missing-directory error', async () => {
    await expect(loadGoldenRoutes(join(tmpdir(), 'does-not-exist-routes-io'))).rejects.toThrow();
  });
});
