# Memory User Interaction Design

## Contract

The protected memory routes require at least the tenant `member` role. `DELETE /memory/clear` derives the tenant and user from authentication and clears only the current user's memory. Admin and owner users have the same personal-memory action as members. The API does not expose a fact-list endpoint, so the frontend must not imply that memories can be browsed or managed individually.

## Interaction

Keep the existing action in the authenticated user menu. Expose the menu trigger and clear action through stable accessible names. Selecting clear opens a destructive confirmation that explains the user-only scope and irreversibility. The confirmation owns its asynchronous promise so Ant Design displays the pending state and prevents duplicate completion. Success shows a short confirmation; failure displays the backend error and remains visible.

The menu remains available to member, admin, and owner roles. It uses the existing responsive application header, so the same button and dialog path works on mobile without adding a separate layout.

## Tests

Component tests cover member and admin visibility, stable role/name lookup, confirmed success, and backend failure. The existing API test continues to freeze the backend-relative route. Frontend lint, typecheck, unit tests, and production build are the acceptance gates.
