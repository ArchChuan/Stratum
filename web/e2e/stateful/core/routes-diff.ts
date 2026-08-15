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
  const withoutQuery = path.split('?')[0] ?? '';
  return withoutQuery
    .split('/')
    .map((segment) => (segment.startsWith(':') ? ':param' : segment))
    .join('/');
};

export const methodPathKey = (method: string, path: string): string =>
  `${method} ${normalizePath(path)}`;

// matchTemplate 用注册模板反向匹配运行时请求:静态段逐一相等,参数段
// (:xxx 或 *xxx)匹配任意单段。命中返回模板自身(即归一化形态)。
// gin 不允许同 method 同静态结构的两个模板并存,故匹配结果唯一。
export const matchTemplate = (
  registered: RouteShape[],
  method: string,
  path: string,
): RouteShape | null => {
  const pathname = path.split('?')[0] ?? '';
  const segments = pathname.split('/').filter(Boolean);
  for (const route of registered) {
    if (route.method !== method) continue;
    const templateSegments = route.path.split('/').filter(Boolean);
    if (templateSegments.length !== segments.length) continue;
    let matched = true;
    for (let i = 0; i < templateSegments.length; i += 1) {
      const templateSegment = templateSegments[i];
      if (templateSegment.startsWith(':') || templateSegment.startsWith('*')) continue;
      if (templateSegment !== segments[i]) {
        matched = false;
        break;
      }
    }
    if (matched) return route;
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
    excluded: excluded.map(({ method, path, reason }) => ({ method, path, reason })),
  };
};
