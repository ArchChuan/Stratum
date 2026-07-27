# Agent Selector System Badge Removal Design

## Goal

Remove the `系统内置` badge from the Agent selector on the Agent chat page so every option is presented by name only.

## Scope

- Simplify labels produced by `ChatConversationSidebar` to the Agent name.
- Preserve the `系统内置` badge in `ChatHeader` for the currently selected system Agent.
- Preserve system Agent ordering, selection, model configuration, readiness guidance, and conversation behavior.

## Implementation

Pass plain string labels to the existing Ant Design `Select`. Remove selector-only `Space`, `Text`, and `Tag` rendering that becomes unused. No API, model, routing, or state changes are required.

## Verification

- Component test: the open selector contains `平台使用小助手` but not `系统内置`.
- Component test: `ChatHeader` continues to render `系统内置` and the settings action.
- Browser test: desktop and mobile selector overlays omit the badge while the selected Agent header retains it.
- Run frontend lint, build, focused tests, and the repository risk guardrails before shipping.
