import { describe, expect, it } from 'vitest';

import {
  buildUncoveredReport, diffRoutes, domainForPath, excludeRoutes, matchTemplate, normalizePath,
  type RouteShape,
} from './routes-diff';

const registered: RouteShape[] = [
  { method: 'GET', path: '/memory' },
  { method: 'POST', path: '/memory/clear' },
  { method: 'POST', path: '/mcp/servers/:serverId/reconnect' },
  { method: 'GET', path: '/agents/:id/execute' },
  { method: 'GET', path: '/health' },
];

describe('normalizePath', () => {
  it('replaces named gin params with :param', () => {
    expect(normalizePath('/agents/:id/execute')).toBe('/agents/:param/execute');
    expect(normalizePath('/mcp/servers/:serverId/:toolName')).toBe('/mcp/servers/:param/:param');
  });

  it('strips query strings', () => {
    expect(normalizePath('/memory?page=1')).toBe('/memory');
  });

  it('leaves static and fixed-value segments untouched', () => {
    expect(normalizePath('/health')).toBe('/health');
    expect(normalizePath('/evaluations/candidates/candidate-1/reject')).toBe(
      '/evaluations/candidates/candidate-1/reject',
    );
  });
});

describe('matchTemplate', () => {
  it('matches a runtime path against its gin template', () => {
    expect(matchTemplate(registered, 'POST', '/mcp/servers/abc-123/reconnect')).toEqual({
      method: 'POST', path: '/mcp/servers/:serverId/reconnect',
    });
    expect(matchTemplate(registered, 'GET', '/agents/abc/execute')).toEqual({
      method: 'GET', path: '/agents/:id/execute',
    });
  });

  it('rejects on method mismatch and structure mismatch', () => {
    expect(matchTemplate(registered, 'GET', '/mcp/servers/abc/reconnect')).toBeNull();
    expect(matchTemplate(registered, 'POST', '/memory')).toBeNull();
    expect(matchTemplate(registered, 'GET', '/memory/clear/extra')).toBeNull();
  });

  it('returns null when no template matches', () => {
    expect(matchTemplate(registered, 'GET', '/not-a-route')).toBeNull();
  });
});

describe('excludeRoutes', () => {
  it('filters registered routes by excluded set', () => {
    const excluded = [{ method: 'GET', path: '/health' }, { method: 'GET', path: '/livez' }];
    const remaining = excludeRoutes(registered, excluded);
    expect(remaining.map((r) => r.path)).toEqual([
      '/memory', '/memory/clear', '/mcp/servers/:serverId/reconnect', '/agents/:id/execute',
    ]);
  });
});

describe('diffRoutes', () => {
  it('reports registered routes not matched by runtime requests', () => {
    const coveredRaw = new Set(['GET /memory', 'POST /memory/clear']);
    const { covered, uncovered } = diffRoutes(registered, coveredRaw, []);
    expect(covered).toEqual(['GET /memory', 'POST /memory/clear']);
    expect(uncovered.map((r) => r.path)).toEqual([
      '/mcp/servers/:param/reconnect',
      '/agents/:param/execute',
      '/health',
    ]);
  });

  it('excludes infra routes and normalizes runtime ids via template match', () => {
    const coveredRaw = new Set([
      'POST /mcp/servers/srv-42/reconnect',
      'GET /agents/alice/execute',
    ]);
    const excluded = [{ method: 'GET', path: '/health' }];
    const { covered, uncovered } = diffRoutes(registered, coveredRaw, excluded);
    // 模板匹配:不同具体 id 归一到同一模板,不进 uncovered。
    expect(covered).toContain('POST /mcp/servers/:param/reconnect');
    expect(covered).toContain('GET /agents/:param/execute');
    expect(uncovered.map((r) => r.path)).toEqual(['/memory', '/memory/clear']);
  });
});

describe('domainForPath', () => {
  it('maps leading path segment to pack domain', () => {
    expect(domainForPath('/mcp/servers/x/reconnect')).toBe('mcp');
    expect(domainForPath('/memory/clear')).toBe('memory');
    expect(domainForPath('/workflow-runs/1')).toBe('workflow');
    expect(domainForPath('/agents/x/execute')).toBe('agent');
    expect(domainForPath('/auth/me')).toBe('iam');
    expect(domainForPath('/dashboard/overview')).toBe('dashboard');
  });

  it('falls back to other for unknown prefixes', () => {
    expect(domainForPath('/unknown-thing/x')).toBe('other');
  });
});

describe('buildUncoveredReport', () => {
  it('assembles a report with uncovered entries and domain hints', () => {
    const registered: RouteShape[] = [
      { method: 'GET', path: '/memory' },
      { method: 'POST', path: '/mcp/servers/:serverId/reconnect' },
      { method: 'GET', path: '/health' },
    ];
    const excluded = [{ method: 'GET', path: '/health', reason: 'infra' }];
    const coveredRaw = new Set(['GET /memory']);
    const report = buildUncoveredReport(registered, coveredRaw, excluded, 'abc123', '2026-08-15T00:00:00Z');
    expect(report.route_total).toBe(3);
    expect(report.generated_at).toBe('2026-08-15T00:00:00Z');
    expect(report.tested_git_parent).toBe('abc123');
    expect(report.covered).toEqual(['GET /memory']);
    expect(report.uncovered).toEqual([
      { method: 'POST', path: '/mcp/servers/:param/reconnect', domain_hint: 'mcp' },
    ]);
    expect(report.excluded).toEqual([{ method: 'GET', path: '/health', reason: 'infra' }]);
  });
});
