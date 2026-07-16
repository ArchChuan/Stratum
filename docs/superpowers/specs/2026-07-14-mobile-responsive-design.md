# Stratum Web Mobile Responsive Design

> **Historical design (implemented).** Current behavior is represented by the responsive components, hooks, styles, and tests under `web/`.

## Goal

Adapt every Stratum web page for common phone widths without changing routes, APIs, permissions, or desktop behavior. The mobile experience must support the complete product, including administrative pages, forms, data lists, and Agent chat.

## Viewports

- Phone: widths below 768px.
- Compact tablet: widths from 768px through 1023px.
- Desktop: widths of 1024px and above; preserve the existing layout and behavior.
- Required phone verification sizes: 320x568, 375x667, 390x844, and 430x932.
- Required desktop regression size: 1440x900.

The page root must not produce horizontal scrolling at any required viewport.

## Architecture

Use shared responsive infrastructure plus page-specific presentation changes. Keep one route tree and one business component/data flow for all viewports. Do not create separate mobile routes or duplicate API and state logic.

Add a shared responsive hook that exposes the established breakpoints. Shared layout primitives should own recurring behavior such as page headers, toolbar wrapping, responsive data presentation, pagination, and mobile overlay sizing. Page-specific components define only their field priority and domain-specific interaction.

## Application Shell

On desktop, retain the fixed collapsible sidebar and current content offset. On phones, remove the fixed sidebar and its content offset. Add a menu icon to the top bar that opens the complete navigation in an Ant Design `Drawer`. Selecting a route closes the drawer.

The mobile top bar retains tenant switching and the user menu while reducing spacing and hiding nonessential status text when space is constrained. The content padding changes from 24px on desktop to 12px on phones. The shell must account for safe-area insets where supported.

The global navigation drawer and Agent conversation drawer are separate controls with distinct labels and state.

## Shared Responsive Behavior

### Page Headers And Toolbars

Desktop headers retain their horizontal layout. Phone headers stack the title/description and actions. Primary actions remain directly visible; secondary actions move to an overflow menu when necessary. Search, filters, and selectors use the available width and wrap without shrinking below usable sizes.

### Data Tables

Desktop and compact tablet layouts retain Ant Design tables where they fit. Phone layouts render the same records as compact information cards. Each domain defines three to five priority fields, normally:

- name or primary identity;
- status;
- one key metric or summary;
- timestamp;
- a compact action menu.

Selecting a card or its detail action opens the full record in a drawer when the existing page does not already provide a detail route. Edit, delete, role changes, and other low-frequency commands remain available without relying on hover. Mobile pagination omits the page-size changer and quick jumper when they cannot fit.

### Forms And Overlays

Forms become single-column on phones. Advanced settings remain collapsed by default. Inputs, selectors, uploads, sliders, and validation messages must fit at 320px. Modal content becomes near-full-screen on phones, with scrollable content and a stable footer. Drawers and popovers must not exceed the viewport width.

Touch targets should be at least 40px in their smallest dimension. Icon-only commands require accessible names and tooltips where the desktop UI also benefits from them.

## Page Rules

### Dashboard

Statistics use a one- or two-column responsive grid. Charts receive stable responsive dimensions. Recent executions switch from a table to information cards on phones.

### Agent, Knowledge, Skill, MCP, IAM, And Tenant Lists

List headers and create actions stack on phones. Filters become full-width. Existing item cards become a single column. Every current table receives a mobile card renderer with domain-appropriate priority fields and complete action access.

### Detail Pages

Detail headers wrap metadata and actions. Upload, query, configuration, and statistics regions become a single column. Long identifiers, URLs, error messages, and generated content wrap or truncate with an explicit way to inspect the full value.

### Agent Chat

The message stream occupies the full phone width and height below the application header. A control in the chat header opens the conversation list in a drawer. The drawer retains Agent selection, conversation creation, selection, rename, and delete operations.

The composer remains visible at the bottom of the usable viewport, respects safe-area insets, and accommodates the mobile software keyboard. At narrow widths the send command uses its icon and accessible label instead of an additional text label. Messages, Markdown, tool steps, and error output must wrap without widening the viewport.

### Authentication And Onboarding

Login, callback, and onboarding containers use a maximum width of the viewport minus 24px. Forms and status content must fit at 320px without horizontal overflow.

## State And Error Handling

Responsive table/card presentations consume the same query state and records. Loading, empty, success, and error behavior remains shared. Layout switching must not trigger duplicate requests or discard unsaved form state.

Existing error messages and confirmation behavior remain in force. Mobile overlays must keep destructive actions clearly separated and preserve confirmation requirements.

## Verification

Run the frontend quality gates:

```bash
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
```

Use Playwright at every required viewport to verify:

- no document-level horizontal overflow;
- the navigation drawer opens, closes, and navigates;
- desktop tables become mobile cards below 768px;
- page actions, filters, pagination, forms, modals, and drawers remain operable;
- the Agent conversation drawer supports selection and management;
- the chat composer stays usable and messages wrap;
- desktop navigation, tables, and page layout do not regress.

After implementation, follow `stratum-e2e-development`: start the real frontend, authenticate through the available test environment, and exercise representative user and administrator routes with browser interactions. This change does not require backend or database modifications.

## Scope Boundaries

- Do not change backend APIs, database schemas, authentication contracts, permissions, or route paths.
- Do not redesign desktop visuals beyond changes required to share responsive components safely.
- Do not create a second mobile application or duplicate page implementations.
- Do not include unrelated refactors discovered during implementation.
