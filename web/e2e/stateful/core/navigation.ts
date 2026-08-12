interface NavigationTarget {
  click(): Promise<void>;
  isVisible(): Promise<boolean>;
}

interface NavigationGroup {
  click(): Promise<void>;
}

export interface AgentNavigationPage {
  getByRole(role: 'link' | 'menuitem', options: { name: string }): NavigationTarget;
  locator(selector: string): { filter(options: { hasText: string }): NavigationGroup };
  waitForURL(url: string): Promise<void>;
}

export const openAgentCreation = async (page: AgentNavigationPage): Promise<void> => {
  // 菜单项是字符串 label,由 antd 渲染为 menuitem 而非 <Link>
  const createItem = page.getByRole('menuitem', { name: '创建 Agent' });
  if (!await createItem.isVisible()) {
    await page.locator('.ant-menu-submenu-title').filter({ hasText: 'Agent' }).click();
  }
  await createItem.click();
  await page.waitForURL('**/agents/create');
};
