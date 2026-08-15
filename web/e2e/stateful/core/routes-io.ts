import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

import { methodPathKey, normalizePath, type RouteShape, type UncoveredReport } from './routes-diff';

// fetchRegisteredRoutes 从后端只读端点拉取注册路由模板全集(gin Routes() dump)。
// fetchFn 可注入以便单测模拟,默认使用全局 fetch。
export const fetchRegisteredRoutes = async (
  baseURL: string,
  fetchFn: typeof fetch = fetch,
): Promise<RouteShape[]> => {
  const response = await fetchFn(`${baseURL}/e2e/routes`);
  if (!response.ok) throw new Error(`GET /e2e/routes failed: ${response.status}`);
  const body = (await response.json()) as { routes: Array<{ method: string; path: string }> };
  return body.routes;
};

// loadGoldenRoutes 解析 golden 契约文件的 (method, path),供 cross-check 并集。
// golden 是数组(同路径多 auth 场景),每个 entry 含 method/path。
// 路径经 normalizePath 归一化;readdir 条目先排序,再按 methodPathKey 去重,
// 最终按 methodPathKey 排序,保证跨平台/文件系统一致的确定性输出。
export const loadGoldenRoutes = async (goldenDir: string): Promise<RouteShape[]> => {
  const entries = await readdir(goldenDir);
  const golden: RouteShape[] = [];
  const seen = new Set<string>();
  for (const entry of entries.filter((name) => name.endsWith('.golden.json')).sort()) {
    const body = JSON.parse(await readFile(join(goldenDir, entry), 'utf8')) as
      Array<{ method?: string; path?: string }>;
    for (const item of body) {
      if (typeof item.method !== 'string' || typeof item.path !== 'string') continue;
      const route = { method: item.method, path: normalizePath(item.path) };
      const key = methodPathKey(route.method, route.path);
      if (seen.has(key)) continue;
      seen.add(key);
      golden.push(route);
    }
  }
  // localeCompare 依赖运行环境 locale,排序需跨平台确定性,故用纯 UTF-16 比较。
  return golden.sort((a, b) => {
    const ka = methodPathKey(a.method, a.path);
    const kb = methodPathKey(b.method, b.path);
    return ka < kb ? -1 : ka > kb ? 1 : 0;
  });
};

// writeReport 将报告 JSON 写入磁盘(仅告警级产物,由 schema 校验脚本守护)。
export const writeReport = async (path: string, report: UncoveredReport): Promise<void> => {
  await writeFile(path, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
};
