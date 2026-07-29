# Platform MCP protocol evidence

- Verified on: 2026-07-29
- Compatibility baseline: `2025-06-18`
- Intermediate version compared: `2025-11-25`
- Current official specification: `2026-07-28` (`/specification/latest` redirected there on the verified date)
- Evidence method: full official pages fetched with HTTP 200 and read from the rendered page body; no search snippets or
  credentials were used.

## Official sources

Authorization:

- <https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization>
- <https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization>
- <https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization>

Tools:

- <https://modelcontextprotocol.io/specification/2025-06-18/server/tools>
- <https://modelcontextprotocol.io/specification/2025-11-25/server/tools>
- <https://modelcontextprotocol.io/specification/2026-07-28/server/tools>

Streamable HTTP and current security guidance:

- <https://modelcontextprotocol.io/specification/2025-06-18/basic/transports>
- <https://modelcontextprotocol.io/specification/2025-11-25/basic/transports>
- <https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http>
- <https://modelcontextprotocol.io/docs/2026-07-28/tutorials/security/security_best_practices#token-passthrough>

## Required contracts

### 1. HTTP authorization responsibility

| Version | Normative claim and anchor | Difference | Stratum decision |
| --- | --- | --- | --- |
| 2025-06-18 | Authorization [`#protocol-requirements`](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#protocol-requirements): HTTP-based implementations **SHOULD** conform to the authorization specification. Tools [`#security-considerations`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#security-considerations): servers **MUST** implement proper access controls. | Baseline separates OAuth transport authorization from tool-level access control. | The Platform MCP validates the token intended for it. Stratum HTTP handlers/application services remain responsible for tenant, role, route, resource, approval, and business authorization. |
| 2025-11-25 | The same requirements remain. Authorization [`#overview`](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#overview) additionally requires protected-resource metadata for authorization-server discovery. | Discovery is strengthened; business authorization is not delegated to MCP metadata. | Keep the same layered responsibility. |
| 2026-07-28 | Authorization [`#protocol-requirements`](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization#protocol-requirements) retains the HTTP **SHOULD**; Tools [`#security-considerations`](https://modelcontextprotocol.io/specification/2026-07-28/server/tools#security-considerations) retains the server access-control **MUST**. | No change to the responsibility relevant to this design. | Keep the compatibility decision. |

### 2. Token passthrough

| Version | Verified claim and anchor | Difference | Stratum decision |
| --- | --- | --- | --- |
| 2025-06-18 | Authorization [`#access-token-privilege-restriction`](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#access-token-privilege-restriction) identifies forwarding unmodified, wrong-audience tokens downstream as a confused-deputy risk. | The page describes the risk; it does not supply a blanket normative sentence permitting passthrough. | Never forward the invocation token to Stratum APIs. Exchange it for a separately scoped API delegation token. |
| 2025-11-25 | The same risk text remains at [`#access-token-privilege-restriction`](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#access-token-privilege-restriction). | No relevant relaxation. | Unchanged. |
| 2026-07-28 | The authorization page no longer contains the old privilege-restriction subsection. Current official security guidance records the same risk at [`#token-passthrough`](https://modelcontextprotocol.io/docs/2026-07-28/tutorials/security/security_best_practices#token-passthrough). | Moved from the authorization page to versioned security guidance. | Unchanged and intentionally stricter than relying on advisory wording alone. |

### 3. Audience and resource binding

| Version | Normative claim and anchor | Difference | Stratum decision |
| --- | --- | --- | --- |
| 2025-06-18 | Authorization [`#resource-parameter-implementation`](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#resource-parameter-implementation): `resource` **MUST** be in authorization and token requests and identify the intended MCP server. [`#token-audience-binding-and-validation`](https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization#token-audience-binding-and-validation): servers **MUST** validate tokens were specifically issued for them. | Compatibility baseline. | Invocation tokens use `aud=stratum-platform-mcp`; API delegation tokens use `aud=stratum-api`. Each verifier rejects the other token. |
| 2025-11-25 | The same requirements remain at the same anchors; [`#canonical-server-uri`](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#canonical-server-uri) makes canonical resource URI handling explicit. | Adds clearer canonicalization and RFC 9728 discovery. | Use a configured canonical internal MCP URI; never infer identity from a tenant-editable URL. |
| 2026-07-28 | [`#resource-parameter-implementation`](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization#resource-parameter-implementation) retains all three `resource` **MUST** requirements. | Resource binding remains current. | Unchanged. |

### 4. Tool input validation and access control

| Version | Normative claim and anchor | Difference | Stratum decision |
| --- | --- | --- | --- |
| 2025-06-18 | Tools [`#security-considerations`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools#security-considerations): servers **MUST** validate all tool inputs, implement access controls, rate-limit invocations, and sanitize outputs. | Baseline contract. | Platform MCP performs strict DTO/schema validation before dispatch and fails closed on authorization or dependency errors. |
| 2025-11-25 | The security requirements remain. [`#tool`](https://modelcontextprotocol.io/specification/2025-11-25/server/tools#tool) additionally requires `inputSchema` to be a valid JSON Schema object and documents the default 2020-12 dialect. | Tightens schema interoperability. | Publish explicit object schemas and reject unknown fields. |
| 2026-07-28 | The security requirements remain. [`#x-mcp-header`](https://modelcontextprotocol.io/specification/2026-07-28/server/tools#x-mcp-header) adds constrained tool-parameter-to-header annotations. | Current tools can require metadata-header behavior not present in the compatibility client. | Do not advertise `x-mcp-header` in the phase-one `2025-06-18` contract; treat current-version support as a separate upgrade. |

### 5. Streamable HTTP session and header behavior used by Stratum

| Version | Normative claim and anchor | Difference | Stratum decision |
| --- | --- | --- | --- |
| 2025-06-18 | Transport [`#sending-messages-to-the-server`](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#sending-messages-to-the-server): POST requests **MUST** accept both JSON and SSE. [`#protocol-version-header`](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#protocol-version-header): subsequent HTTP requests **MUST** carry the negotiated `MCP-Protocol-Version`. [`#session-management`](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#session-management): a returned `Mcp-Session-Id` **MUST** be sent on subsequent requests. | Compatibility baseline implemented by `BaseClient`. | Negotiate `2025-06-18`; send `Content-Type: application/json`, `Accept: application/json, text/event-stream`, Bearer authorization, W3C trace context, negotiated protocol version after initialize, and returned session ID. |
| 2025-11-25 | The same protocol/session requirements remain; header spelling is rendered as `MCP-Session-Id`, and secure handling is made explicit. | Streaming reconnect details expand, but the request headers used by Stratum remain compatible. | Preserve the baseline behavior; HTTP header names are case-insensitive. |
| 2026-07-28 | Current Streamable HTTP [`#protocol-version-header`](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http#protocol-version-header) requires the protocol header on **every** POST. [`#standard-request-headers`](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http#standard-request-headers) adds required `Mcp-Method` and conditional `Mcp-Name`. [`#earlier-streamable-http-revisions`](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http#earlier-streamable-http-revisions) says current-only servers ignore session IDs and do not mint them. | This is not wire-compatible with the initialized, session-based 2025 revisions. | Freeze phase one at `2025-06-18`. A future current-version upgrade must add request `_meta`, mirrored metadata headers, header/body validation, and remove session assumptions as one tested change. |

W3C `traceparent`/`tracestate` are Stratum observability headers, not MCP normative headers. They are injected from the
request context and are covered by the compatibility test alongside the MCP and OAuth headers.

## Stratum decisions

- MCP configuration is not identity.
- Platform credentials are issued per tool call.
- Invocation tokens are not passed through to downstream APIs.
- Business authorization remains in Stratum HTTP handlers/application services.
- Phase one intentionally implements the `2025-06-18` Streamable HTTP compatibility contract; `2026-07-28` support is
  a separate protocol upgrade, not an implicit reinterpretation of the existing client.
