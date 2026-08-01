import { describe, expect, it, vi } from 'vitest';

import { openAgentCreation } from '../../e2e/stateful/core/navigation';

describe('openAgentCreation', () => {
  it('uses the visible application navigation without reloading the document', async () => {
    const createLink = { click: vi.fn().mockResolvedValue(undefined), isVisible: vi.fn().mockResolvedValue(false) };
    const agentGroup = { click: vi.fn().mockResolvedValue(undefined) };
    const page = {
      getByRole: vi.fn().mockReturnValue(createLink),
      locator: vi.fn().mockReturnValue({ filter: vi.fn().mockReturnValue(agentGroup) }),
      waitForURL: vi.fn().mockResolvedValue(undefined),
    };

    await openAgentCreation(page);

    expect(agentGroup.click).toHaveBeenCalledOnce();
    expect(createLink.click).toHaveBeenCalledOnce();
    expect(page.waitForURL).toHaveBeenCalledWith('**/agents/create');
  });
});
