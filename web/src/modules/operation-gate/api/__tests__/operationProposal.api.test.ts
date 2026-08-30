import { beforeEach, describe, expect, it, vi } from 'vitest';

import { operationProposalApi, type GrantableResourceType } from '../operationProposal.api';

const post = vi.hoisted(() => vi.fn());

vi.mock('@/services/client', () => ({
  default: { post },
}));

interface URLCase {
  name: string;
  resourceType: GrantableResourceType;
  resourceId: string;
  options?: { workspaceName?: string; resourceName?: string };
  url: string;
}

const urlCases: URLCase[] = [
  { name: 'agent 走默认拼接', resourceType: 'agent', resourceId: 'a1', url: '/agents/a1/request-editor' },
  { name: 'skill 走默认拼接', resourceType: 'skill', resourceId: 's1', url: '/skills/s1/request-editor' },
  {
    name: 'knowledge_doc 带 workspaceName 定位到文档',
    resourceType: 'knowledge_doc',
    resourceId: 'd1',
    options: { workspaceName: 'ws-1', resourceName: 'docs/annual.pdf' },
    url: '/knowledge/workspaces/ws-1/documents/d1/request-access',
  },
  {
    name: 'knowledge_doc 缺 workspaceName 按当前实现以空串占位',
    resourceType: 'knowledge_doc',
    resourceId: 'd1',
    url: '/knowledge/workspaces//documents/d1/request-access',
  },
  { name: 'knowledge_workspace 按 id 拼接', resourceType: 'knowledge_workspace', resourceId: 'kw-1', url: '/knowledge/workspaces/kw-1/request-editor' },
  { name: 'mcp 走默认拼接', resourceType: 'mcp', resourceId: 'm1', url: '/mcp/servers/m1/request-editor' },
  { name: 'workflow 走默认拼接', resourceType: 'workflow', resourceId: 'w1', url: '/workflows/w1/request-editor' },
];

describe('operationProposalApi.requestEditorAccess', () => {
  beforeEach(() => {
    post.mockReset();
    post.mockResolvedValue({ data: { status: 'pending_approval' } });
  });

  it.each(urlCases)('$name → $url', async ({ resourceType, resourceId, options, url }) => {
    await operationProposalApi.requestEditorAccess(resourceType, resourceId, options);
    expect(post).toHaveBeenCalledWith(url, expect.anything());
  });

  it('body 只发 resourceType + resourceName，不带 resourceId（后端 DisallowUnknownFields）', async () => {
    await operationProposalApi.requestEditorAccess('agent', 'a1', { resourceName: 'My Agent' });
    expect(post).toHaveBeenCalledWith(
      '/agents/a1/request-editor',
      { resourceType: 'agent', resourceName: 'My Agent' },
    );
    const body = post.mock.calls[0][1] as Record<string, unknown>;
    expect(body).not.toHaveProperty('resourceId');
    expect(Object.keys(body).sort()).toEqual(['resourceName', 'resourceType']);
  });
});
