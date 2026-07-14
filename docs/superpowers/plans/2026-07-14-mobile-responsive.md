# Stratum Web Mobile Responsive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every Stratum web route usable at common phone widths while preserving the current desktop application and business behavior.

**Architecture:** Add one `matchMedia`-backed responsive hook and shared CSS/layout primitives, then let domain components switch presentation at the 768px phone breakpoint. Desktop tables and sidebars remain intact; phones use navigation/conversation drawers and compact record cards backed by the same state and callbacks.

**Tech Stack:** React 18, TypeScript, Ant Design 5, CSS media queries, Vitest/Testing Library, Playwright.

---

## Task 1: Responsive Foundation

**Files:**

- Create: `web/src/shared/hooks/useResponsive.ts`
- Create: `web/src/shared/hooks/__tests__/useResponsive.test.tsx`
- Modify: `web/src/shared/hooks/index.ts`
- Modify: `web/src/index.css`

- [ ] **Step 1: Write a failing hook test**

Mock `window.matchMedia`, render the hook, and assert that widths below 768px expose `isMobile=true`, widths below 1024px expose `isCompact=true`, and media-query change events update the result.

```tsx
const { result } = renderHook(() => useResponsive());
expect(result.current).toEqual({ isMobile: true, isCompact: true });
act(() => listeners.get('(max-width: 767px)')?.({ matches: false } as MediaQueryListEvent));
expect(result.current.isMobile).toBe(false);
```

- [ ] **Step 2: Run the test and verify the missing export failure**

Run: `npm --prefix web test -- src/shared/hooks/__tests__/useResponsive.test.tsx`

Expected: FAIL because `useResponsive` does not exist.

- [ ] **Step 3: Implement the shared hook and CSS contract**

Implement a `useMediaQuery(query)` helper with `addEventListener('change', listener)` cleanup, then export:

```ts
export const useResponsive = () => ({
  isMobile: useMediaQuery('(max-width: 767px)'),
  isCompact: useMediaQuery('(max-width: 1023px)'),
});
```

Replace Vite starter styles in `index.css` with application-safe defaults and add reusable classes: `.app-shell-content`, `.responsive-page-header`, `.responsive-toolbar`, `.responsive-grid`, `.mobile-only`, `.desktop-only`, `.mobile-overlay`, `.mobile-card-list`, and safe-area padding. At `max-width: 767px`, set content padding to 12px, stack page headers/toolbars, use one grid column, make form controls fluid, constrain Ant Design overlays, and prevent long text/code from widening the viewport.

- [ ] **Step 4: Run focused verification**

Run: `npm --prefix web test -- src/shared/hooks/__tests__/useResponsive.test.tsx && npm --prefix web run typecheck`

Expected: PASS with no TypeScript errors.

- [ ] **Step 5: Commit the foundation**

```bash
git add web/src/shared/hooks web/src/index.css
git commit -m "feat(web): add responsive layout foundation"
```

## Task 2: Responsive Application Shell

**Files:**

- Create: `web/src/app/layout/__tests__/AppShell.test.tsx`
- Modify: `web/src/app/layout/AppShell.tsx`
- Modify: `web/src/app/layout/menu.config.tsx`

- [ ] **Step 1: Write failing shell interaction tests**

Mock `useResponsive`, router, authentication, and `/health`. Verify that mobile rendering has a labelled navigation button, no fixed sider, and a drawer that closes after selecting a route. Verify desktop rendering keeps the sider.

```tsx
expect(screen.getByRole('button', { name: '打开主导航' })).toBeInTheDocument();
await user.click(screen.getByRole('button', { name: '打开主导航' }));
expect(screen.getByRole('dialog', { name: '主导航' })).toBeInTheDocument();
```

- [ ] **Step 2: Run the shell test and verify it fails**

Run: `npm --prefix web test -- src/app/layout/__tests__/AppShell.test.tsx`

Expected: FAIL because the mobile navigation control is absent.

- [ ] **Step 3: Implement the drawer shell**

Use `useResponsive()` and a `mobileNavOpen` state. Extract the existing logo/menu body into a local `NavigationContent` component. Render `Sider` only when `!isMobile`; on mobile render a `MenuOutlined` icon button and an Ant Design `Drawer` with `width="min(84vw, 320px)"`. Apply zero content offset on mobile, keep 80/220px offsets on desktop, close the drawer in the menu `onClick`, and give menu and close controls accessible labels.

Keep tenant switching and `UserMenu`; hide the connection text at 320px while retaining the badge's accessible status. Make the create-tenant modal use full-width mobile sizing.

- [ ] **Step 4: Verify shell behavior**

Run: `npm --prefix web test -- src/app/layout/__tests__/AppShell.test.tsx && npm --prefix web run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit the shell**

```bash
git add web/src/app/layout
git commit -m "feat(web): add mobile navigation drawer"
```

## Task 3: Shared Page, Form, And Mobile Record Primitives

**Files:**

- Create: `web/src/shared/ui/ResponsiveDataView.tsx`
- Create: `web/src/shared/ui/__tests__/ResponsiveDataView.test.tsx`
- Modify: `web/src/shared/ui/index.ts`
- Modify: `web/src/shared/ui/ResourceListPage.tsx`
- Modify: `web/src/shared/ui/FormPage.tsx`
- Modify: `web/src/shared/ui/SectionHeader.tsx`

- [ ] **Step 1: Write a failing responsive data-view test**

Define `rows`, desktop columns, and a `renderMobileItem` function. Assert the table is rendered on desktop and cards are rendered on mobile without changing row data or action callbacks.

```tsx
render(<ResponsiveDataView rows={rows} columns={columns} rowKey="id" renderMobileItem={renderCard} />);
expect(screen.getByTestId('mobile-record-a')).toBeInTheDocument();
expect(screen.queryByRole('table')).not.toBeInTheDocument();
```

- [ ] **Step 2: Run the test and verify the missing component failure**

Run: `npm --prefix web test -- src/shared/ui/__tests__/ResponsiveDataView.test.tsx`

Expected: FAIL because `ResponsiveDataView` does not exist.

- [ ] **Step 3: Implement shared responsive primitives**

Create a generic component with props extending the subset of `TableProps<T>` used by the project:

```ts
interface ResponsiveDataViewProps<T extends object> {
  rows: T[];
  columns: ColumnsType<T>;
  rowKey: keyof T | ((row: T) => React.Key);
  loading?: boolean;
  pagination?: TablePaginationConfig | false;
  onChange?: TableProps<T>['onChange'];
  emptyText: string;
  renderMobileItem: (row: T) => ReactNode;
}
```

Render the existing `Table<T>` on desktop and a loading skeleton, `EmptyHint`, mobile card list, and compact `Pagination` on phones. Update shared page headers and form action rows to use the CSS contract. `FormPage` buttons become full-width at phone widths without changing submit behavior.

- [ ] **Step 4: Verify shared primitives**

Run: `npm --prefix web test -- src/shared/ui && npm --prefix web run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit shared UI**

```bash
git add web/src/shared/ui
git commit -m "feat(web): add responsive page primitives"
```

## Task 4: Convert Dashboard And Domain Tables To Mobile Cards

**Files:**

- Modify: `web/src/modules/dashboard/pages/DashboardPage.tsx`
- Modify: `web/src/modules/dashboard/components/RecentExecutionsTable.tsx`
- Modify: `web/src/modules/agent/components/ExecutionHistoryTable.tsx`
- Modify: `web/src/modules/iam/components/TenantMemberTable.tsx`
- Modify: `web/src/modules/iam/pages/admin/TenantsListPage.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceDocumentsTable.tsx`
- Modify: `web/src/modules/mcp/pages/MCPServersPage.tsx`
- Modify: `web/src/modules/mcp/components/ServerDetailDrawer.tsx`
- Create: `web/src/shared/ui/__tests__/mobileDataViews.test.tsx`

- [ ] **Step 1: Write failing representative card tests**

Use the existing test setup and mock `useResponsive` as mobile. Cover execution, member, document, and MCP records. Assert that each card exposes its identity, status/key metric, time or summary, and all authorized actions.

```tsx
expect(screen.getByText('研究助手')).toBeInTheDocument();
expect(screen.getByText('成功')).toBeInTheDocument();
expect(screen.getByRole('button', { name: '查看研究助手详情' })).toBeInTheDocument();
```

- [ ] **Step 2: Run the test and verify current table-only rendering fails**

Run: `npm --prefix web test -- src/shared/ui/__tests__/mobileDataViews.test.tsx`

Expected: FAIL because mobile cards are absent.

- [ ] **Step 3: Migrate all eight table sites**

Use `ResponsiveDataView` at each site. Keep desktop columns unchanged. Define domain cards with these priorities:

- executions: Agent, status, input/error summary, tokens/duration, time;
- members: avatar/login, role, joined date, role/remove actions;
- tenants: name/ID, status, member count, created time, actions;
- documents: filename, status, size/chunks, uploaded time, actions;
- MCP servers: name, transport, enabled state, endpoint summary, actions;
- server tools/resources: name, description or URI/MIME, detail access.

Use icon menus for dense mobile actions and preserve `DangerPopconfirm` for destructive commands. Dashboard statistic cards use `xs=24`, `sm=12`, and existing desktop spans.

- [ ] **Step 4: Verify domain data views**

Run: `npm --prefix web test -- src/shared/ui/__tests__/mobileDataViews.test.tsx && npm --prefix web run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit table conversions**

```bash
git add web/src/modules/dashboard web/src/modules/agent/components/ExecutionHistoryTable.tsx web/src/modules/iam web/src/modules/knowledge/components/WorkspaceDocumentsTable.tsx web/src/modules/mcp web/src/shared/ui/__tests__/mobileDataViews.test.tsx
git commit -m "feat(web): render domain data as mobile cards"
```

## Task 5: Adapt Lists, Forms, Detail Pages, And Authentication

**Files:**

- Modify: `web/src/modules/agent/pages/AgentsListPage.tsx`
- Modify: `web/src/modules/agent/pages/CreateAgentPage.tsx`
- Modify: `web/src/modules/agent/pages/EditAgentPage.tsx`
- Modify: `web/src/modules/agent/pages/ExecutionHistoryPage.tsx`
- Modify: `web/src/modules/knowledge/pages/KnowledgePage.tsx`
- Modify: `web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx`
- Modify: `web/src/modules/skill/pages/SkillsListPage.tsx`
- Modify: `web/src/modules/skill/pages/CreateSkillPage.tsx`
- Modify: `web/src/modules/skill/pages/EditSkillPage.tsx`
- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.tsx`
- Modify: `web/src/modules/mcp/pages/CreateMCPPage.tsx`
- Modify: `web/src/modules/mcp/pages/EditMCPPage.tsx`
- Modify: `web/src/modules/iam/pages/auth/LoginPage.tsx`
- Modify: `web/src/modules/iam/pages/auth/CallbackPage.tsx`
- Modify: `web/src/modules/iam/pages/auth/OnboardingPage.tsx`
- Modify: `web/src/modules/iam/pages/tenant/MembersPage.tsx`
- Modify: `web/src/modules/iam/pages/tenant/SettingsPage.tsx`
- Modify: `web/src/modules/agent/components/AgentsListFilters.tsx`
- Modify: `web/src/modules/agent/components/AgentFormSections.tsx`
- Modify: `web/src/modules/agent/components/AgentCard.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceCard.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceCreateModal.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceDetailHeader.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceListGrid.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceListHeader.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceQueryPanel.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceQueryResult.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceStatsCard.tsx`
- Modify: `web/src/modules/knowledge/components/WorkspaceUploadZone.tsx`
- Modify: `web/src/modules/skill/components/SkillFormSections.tsx`
- Modify: `web/src/modules/skill/components/SkillCard.tsx`
- Modify: `web/src/modules/mcp/components/MCPBasicSection.tsx`
- Modify: `web/src/modules/mcp/components/MCPAuthSection.tsx`
- Modify: `web/src/modules/mcp/components/MCPRetrySection.tsx`
- Modify: `web/src/modules/mcp/components/MCPTransportSection.tsx`
- Create: `web/src/shared/ui/__tests__/responsivePages.test.tsx`

- [ ] **Step 1: Write failing layout-contract tests**

Render representative list, create/edit, knowledge detail, settings, login, and onboarding pages at mobile width. Assert fluid controls, stacked action groups, and viewport-constrained auth cards using semantic classes or `data-testid` attributes instead of pixel snapshots.

- [ ] **Step 2: Run the page tests and verify current fixed-width layouts fail**

Run: `npm --prefix web test -- src/shared/ui/__tests__/responsivePages.test.tsx`

Expected: FAIL on fixed filter, auth-card, and multi-column form layouts.

- [ ] **Step 3: Apply the shared responsive contract**

Replace inline fixed widths with `width: '100%'` plus desktop `maxWidth` where required. Apply responsive page-header and toolbar classes. Make card grids one column on phones. Convert paired form rows in `SkillFormSections` and configuration panels to responsive CSS grids. Ensure detail headers wrap, upload/query/config areas stack, and long identifiers use `overflow-wrap:anywhere`.

Set login and onboarding cards to `width: 'min(100%, 440px)'` with 12px viewport gutters. Configure every page modal with phone-safe width/body scrolling and every drawer with `width={isMobile ? '100%' : existingWidth}` where full-screen behavior is appropriate.

- [ ] **Step 4: Verify pages and forms**

Run: `npm --prefix web test -- src/shared/ui/__tests__/responsivePages.test.tsx && npm --prefix web run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit page adaptation**

```bash
git add web/src/modules web/src/shared/ui/__tests__/responsivePages.test.tsx
git commit -m "feat(web): adapt pages and forms for phones"
```

## Task 6: Mobile Agent Chat

**Files:**

- Modify: `web/src/modules/agent/pages/AgentChatPage.tsx`
- Modify: `web/src/modules/agent/components/ChatConversationSidebar.tsx`
- Modify: `web/src/modules/agent/components/ChatHeader.tsx`
- Modify: `web/src/modules/agent/components/ChatMessageList.tsx`
- Modify: `web/src/modules/agent/components/ChatMarkdown.tsx`
- Modify: `web/src/modules/agent/components/ChatComposer.tsx`
- Create: `web/src/modules/agent/components/__tests__/AgentChatMobile.test.tsx`

- [ ] **Step 1: Write failing mobile-chat tests**

Mock `useChatPage` with two conversations. Assert that mobile chat hides the permanent conversation sidebar, exposes `打开会话列表`, opens a drawer, selects a conversation and closes the drawer, and renders an icon-only send control with the accessible name `发送消息`.

- [ ] **Step 2: Run the test and verify it fails**

Run: `npm --prefix web test -- src/modules/agent/components/__tests__/AgentChatMobile.test.tsx`

Expected: FAIL because the sidebar is permanently visible and no conversation drawer exists.

- [ ] **Step 3: Implement mobile chat behavior**

Add `conversationOpen` state in `AgentChatPage`. Render the existing sidebar on desktop and the same sidebar content inside a full-height drawer on phones. Pass an `onOpenConversations` callback to `ChatHeader`. Close the drawer after `onSelectConv`.

Use `100dvh` with a `100vh` fallback for chat height. Add safe-area padding to the composer, reduce phone message horizontal padding, change Markdown/message max width from 72% to 88% on phones, and apply `overflow-wrap:anywhere` to code, links, tool output, and errors. Keep Enter/Shift+Enter behavior and all stream state unchanged.

- [ ] **Step 4: Verify chat**

Run: `npm --prefix web test -- src/modules/agent/components/__tests__/AgentChatMobile.test.tsx && npm --prefix web run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit chat adaptation**

```bash
git add web/src/modules/agent
git commit -m "feat(web): add mobile Agent chat layout"
```

## Task 7: Cross-Viewport E2E And Final Verification

**Files:**

- Create: `web/e2e/responsive.spec.ts`
- Modify: `web/playwright.config.ts`

- [ ] **Step 1: Add failing Playwright overflow and interaction coverage**

Define phone projects for 320x568, 375x667, 390x844, and 430x932 plus desktop 1440x900. Use authenticated test state or route interception already accepted by the test environment. For representative user and admin routes, assert:

```ts
const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
expect(overflow).toBeLessThanOrEqual(1);
```

Also exercise the main navigation drawer, a mobile data card/detail action, a modal/form, the chat conversation drawer, message input, and desktop sider/table visibility.

- [ ] **Step 2: Run Playwright against the implementation**

Run: `npm --prefix web run dev -- --host 0.0.0.0` in a managed session, then `npx --prefix web playwright test web/e2e/responsive.spec.ts --reporter=list`.

Expected before final fixes: any remaining overflow or inaccessible control produces a specific failing route and viewport.

- [ ] **Step 3: Fix only evidenced responsive defects**

Trace each failing element to its owning component, apply the shared class or local field-priority correction, and rerun the failing Playwright project before continuing. Do not add blanket `overflow-x:hidden` to conceal overflowing controls.

- [ ] **Step 4: Run all frontend quality gates**

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
npx --prefix web playwright test web/e2e/responsive.spec.ts --reporter=list
```

Expected: every command exits 0.

- [ ] **Step 5: Perform Stratum E2E closeout**

Follow `stratum-e2e-development`: use the real local frontend and available test authentication, navigate representative user and administrator routes, operate navigation, filters, create/edit overlays, data actions, and Agent chat at phone and desktop sizes, and confirm no sensitive values are printed. Stop all processes started for verification.

- [ ] **Step 6: Commit E2E coverage and final fixes**

```bash
git add web
git commit -m "test(web): cover responsive user workflows"
```
