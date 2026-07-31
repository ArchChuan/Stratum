interface NavigationTarget {
  click(): Promise<void>;
  isVisible(): Promise<boolean>;
}

interface NavigationGroup {
  click(): Promise<void>;
}

export interface AgentNavigationPage {
  getByRole(role: 'link', options: { name: string }): NavigationTarget;
  locator(selector: string): { filter(options: { hasText: string }): NavigationGroup };
  waitForURL(url: string): Promise<void>;
}

export const openAgentCreation = async (page: AgentNavigationPage): Promise<void> => {
  const createLink = page.getByRole('link', { name: '创建 Agent' });
  if (!await createLink.isVisible()) {
    await page.locator('.ant-menu-submenu-title').filter({ hasText: 'Agent' }).click();
  }
  await createLink.click();
  await page.waitForURL('**/agents/create');
};
