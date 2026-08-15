export interface RouteShape {
  method: string;
  path: string;
}

// DOMAIN_PREFIXES maps leading path segments to stateful pack domain names.
// 与 test/e2e/stateful/manifest.json 的 domain 取值保持一致。
const DOMAIN_PREFIXES: Array<[string, string]> = [
  ['/workflow-runs', 'workflow'],
  ['/workflow-approvals', 'workflow'],
  ['/workflows', 'workflow'],
  ['/memory', 'memory'],
  ['/mcp', 'mcp'],
  ['/skills', 'skill'],
  ['/knowledge', 'knowledge'],
  ['/agents', 'agent'],
  ['/evaluations', 'evaluation'],
  ['/scheduled-tasks', 'scheduled-task'],
  ['/collaborations', 'collab'],
  ['/auth', 'iam'],
  ['/tenant', 'iam'],
  ['/admin', 'llm-admin'],
  ['/audit', 'audit'],
  ['/dashboard', 'dashboard'],
];

// normalizePath 去 query,并把 gin 具名参数段(:xxx)统一为 :param。
// 固定值段(如 candidate-1)不做启发式猜测——运行时 id 的归一化由 matchTemplate 完成。
export const normalizePath = (path: string): string => {
  const withoutQuery = path.split('?')[0];
  return withoutQuery
    .split('/')
    .map((segment) => (segment.startsWith(':') ? ':param' : segment))
    .join('/');
};

export const methodPathKey = (method: string, path: string): string =>
  `${method} ${normalizePath(path)}`;

const segmentsOf = (path: string): string[] => path.split('/').filter(Boolean);

const isStaticTemplate = (path: string): boolean =>
  segmentsOf(path).every((segment) => !segment.startsWith(':') && !segment.startsWith('*'));

const templateMatches = (templatePath: string, segments: string[]): boolean => {
  const templateSegments = segmentsOf(templatePath);
  if (templateSegments.length !== segments.length) return false;
  return templateSegments.every((templateSegment, i) => {
    // 参数段(:xxx 或 *xxx)匹配任意单段;静态段逐一相等。
    if (templateSegment.startsWith(':') || templateSegment.startsWith('*')) return true;
    return templateSegment === segments[i];
  });
};

// matchTemplate 用注册模板反向匹配运行时请求,命中返回模板自身(即归一化形态)。
// 两遍扫描:先匹配纯静态模板,再匹配含参数模板。gin 允许 /agents/:id 与
// /agents/me 并存,静态路由注册顺序可能晚于参数路由;先扫静态可避免请求
// /agents/me 被 :param 模板误吞。gin 不允许同 method 同静态结构并存,故
// 同一遍内匹配结果唯一。
export const matchTemplate = (
  registered: RouteShape[],
  method: string,
  path: string,
): RouteShape | null => {
  const pathname = path.split('?')[0];
  const segments = pathname.split('/').filter(Boolean);
  const sameMethod = registered.filter((route) => route.method === method);
  for (const route of sameMethod) {
    if (isStaticTemplate(route.path) && templateMatches(route.path, segments)) return route;
  }
  for (const route of sameMethod) {
    if (!isStaticTemplate(route.path) && templateMatches(route.path, segments)) return route;
  }
  return null;
};

export const excludeRoutes = (registered: RouteShape[], excluded: RouteShape[]): RouteShape[] => {
  const excludedKeys = new Set(excluded.map(({ method, path }) => methodPathKey(method, path)));
  return registered.filter(({ method, path }) => !excludedKeys.has(methodPathKey(method, path)));
};

export const diffRoutes = (
  registered: RouteShape[],
  coveredRaw: Set<string>,
  excluded: RouteShape[],
): { covered: string[]; uncovered: RouteShape[] } => {
  const candidates = excludeRoutes(registered, excluded);
  const covered = new Set<string>();
  for (const raw of coveredRaw) {
    const space = raw.indexOf(' ');
    if (space <= 0) continue;
    const method = raw.slice(0, space);
    const path = raw.slice(space + 1);
    const matched = matchTemplate(candidates, method, path);
    if (matched) covered.add(methodPathKey(matched.method, matched.path));
  }
  const uncovered: RouteShape[] = [];
  for (const route of candidates) {
    if (covered.has(methodPathKey(route.method, route.path))) continue;
    uncovered.push({ method: route.method, path: normalizePath(route.path) });
  }
  return { covered: [...covered].sort(), uncovered };
};

export const domainForPath = (path: string): string => {
  const normalized = normalizePath(path);
  for (const [prefix, domain] of DOMAIN_PREFIXES) {
    if (normalized.startsWith(prefix)) return domain;
  }
  return 'other';
};

export type ExcludedRoute = RouteShape & { reason: string };

export interface UncoveredReport {
  generated_at: string;
  tested_git_parent: string;
  route_total: number;
  covered: string[];
  uncovered: Array<{ method: string; path: string; domain_hint: string }>;
  excluded: Array<{ method: string; path: string; reason: string }>;
}

export const buildUncoveredReport = (
  registered: RouteShape[],
  coveredRaw: Set<string>,
  excluded: ExcludedRoute[],
  gitParent: string,
  generatedAt: string,
): UncoveredReport => {
  const { covered, uncovered } = diffRoutes(registered, coveredRaw, excluded);
  return {
    generated_at: generatedAt,
    tested_git_parent: gitParent,
    route_total: registered.length,
    covered,
    uncovered: uncovered.map(({ method, path }) => ({
      method,
      path,
      domain_hint: domainForPath(path),
    })),
    excluded,
  };
};

// mergeGoldenRoutes 把 golden 契约路径并入注册集:能匹配到已注册模板的忽略,
// 匹配不到的(golden 漏记或 dump 缺路由)按归一化形态追加为额外待覆盖项。
// golden 同一路径含多 auth 场景,追加前按 (method, path) 去重。
export const mergeGoldenRoutes = (
  registered: RouteShape[],
  goldenRoutes: RouteShape[],
): RouteShape[] => {
  const extra: RouteShape[] = [];
  const seen = new Set(registered.map(({ method, path }) => methodPathKey(method, path)));
  for (const golden of goldenRoutes) {
    if (matchTemplate(registered, golden.method, golden.path)) continue;
    const key = methodPathKey(golden.method, golden.path);
    if (seen.has(key)) continue;
    seen.add(key);
    extra.push({ method: golden.method, path: normalizePath(golden.path) });
  }
  return [...registered, ...extra];
};
