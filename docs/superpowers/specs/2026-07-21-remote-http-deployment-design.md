# Remote HTTP Deployment Design

## Goal

Support the current remote single-host K3s deployment at
`http://<public-ip>:6879`. The network edge forwards public TCP 6879 to the
host's HTTP port 80. The deployment has no domain name and no TLS certificate.

This is a constrained exception for the remote demo environment. It must not
weaken SSH host verification, Kubernetes API TLS verification, database TLS
requirements, secret handling, or the HTTPS production profile.

## Current State

- `.github/workflows/deploy.yml` always deploys `helm/values-demo.yaml`.
- `helm/values-demo.yaml` requires a hostname, the Traefik `websecure`
  entrypoint, TLS, secure cookies, and placeholder HTTPS URLs.
- `scripts/quality/check-deployment-safety-test.sh` rejects every non-localhost
  HTTP URL in both demo values files.
- The Helm Ingress template already omits `spec.rules[].host` when the value is
  empty, so it can render a hostless IP-access ingress without template changes.
- The frontend proxies `/api/` to the in-cluster backend. Internal frontend and
  backend services correctly remain on port 80; the backend process remains on
  port 8080.
- `FRONTEND_URL` controls CORS and post-login redirects.
  `GITHUB_CALLBACK_URL` controls the OAuth callback, and `SECURE_COOKIES`
  controls OAuth state and refresh cookie transport flags.
- Historical project documentation describes the same remote HTTP/IP shape,
  but current values and safety checks were later changed to require HTTPS.

## Evidence Boundary

Repository code, tests, configuration, and Git history are the authority for
the current implementation. The relevant Obsidian result is marked
`provisional` and only reinforces the general principle that delivery needs
layered validation; it does not decide this deployment design.

Official Kubernetes Ingress and GitHub OAuth documentation must be checked
before implementation for hostless Ingress and callback URL semantics. The
repository hook blocked that read-only fetch during brainstorming, so those
external claims are not treated as verified in this design.

## Chosen Approach

Add a dedicated remote HTTP values profile and keep the existing HTTPS demo
profile intact. This makes the exception explicit and testable, and leaves a
ready migration path when a domain and certificate become available.

Rejected alternatives:

- Converting `values-demo.yaml` back to HTTP would erase the existing HTTPS
  deployment profile and broaden the exception.
- Expressing the complete deployment only through CI `--set` flags would hide
  the rendered architecture from normal Helm lint and template checks.

## Configuration Design

Create `helm/values-demo-remote-http.yaml` as an overlay on top of
`helm/values-demo.yaml`. The workflow will pass both files in order, with the
HTTP overlay last.

The overlay will set:

- `config.secureCookies: "false"`;
- Ingress class `traefik` with entrypoint `web`;
- one empty host so the Ingress matches requests by IP;
- an empty TLS list.

The overlay will not contain a public IP or duplicate dependency/image/resource
settings. The public address comes from the GitHub Production Environment
variable `PUBLIC_BASE_URL`.

The workflow validates `PUBLIC_BASE_URL` before Helm runs. Its accepted form is
exactly `http://<IP-literal>:6879` with no path, query, fragment, credentials,
or trailing slash. DNS names, other ports, HTTPS values, and malformed input
fail the deployment. IPv4 is required for the current host; IPv6 support is out
of scope until there is a concrete deployment need.

After validation, Helm receives:

```text
config.frontendUrl=$PUBLIC_BASE_URL
config.githubCallbackUrl=$PUBLIC_BASE_URL/api/auth/github/callback
config.secureCookies=false
```

`PUBLIC_BASE_URL` is deployment metadata, not a credential. It is a GitHub
Environment variable rather than a repository value so the repository remains
portable across hosts.

## Request Flow

```text
browser http://PUBLIC_IP:6879
  -> cloud/firewall port forward to host port 80
  -> K3s Traefik `web` entrypoint
  -> hostless Ingress
  -> frontend Service port 80
  -> frontend nginx
       /api/* -> backend Service port 80 -> backend process port 8080
       /*     -> React application
```

No Kubernetes Service, application listener, or Traefik internal port changes
are required. Port 6879 exists only in the browser-visible base URL and the
external forwarding rule.

## Safety Contract

Replace the blanket "remote demo HTTP" rejection with profile-specific
contracts:

- `helm/values-demo.yaml` remains HTTPS-only, uses `websecure`, enables secure
  cookies, and declares TLS.
- `helm/values-prod.yaml` retains its existing TLS and database protections.
- `helm/values-demo-local.yaml` remains localhost-only.
- `helm/values-demo-remote-http.yaml` is the only remote HTTP exception and
  must use `web`, have no TLS, use an empty host, and disable secure cookies.
- The deployment workflow must validate and inject `PUBLIC_BASE_URL`; the HTTP
  overlay must not hard-code an IP.
- Existing SSH, kubeconfig, immutable image, secret checksum, scanner, and
  database TLS checks remain unchanged.

The safety check should validate structured intent with focused assertions
rather than merely deleting the current rejection.

## OAuth And Cookie Behavior

HTTP requires `Secure=false` for the OAuth state and refresh cookies; otherwise
browsers will not return them over this deployment's transport. Cookies remain
`HttpOnly` where the current handlers set that property.

The GitHub OAuth App callback must be configured to exactly match:

```text
http://<public-ip>:6879/api/auth/github/callback
```

The deployment cannot update that external GitHub App setting. Verification
must distinguish "site and health endpoint work" from "OAuth login works" so a
callback mismatch cannot be reported as a successful end-to-end deployment.

HTTP exposes browser traffic and session material to interception on untrusted
networks. This design accepts that stated environment constraint; it does not
describe HTTP as equivalent to HTTPS. Moving to a domain and TLS means selecting
the existing HTTPS profile and setting secure cookies back to true.

## Failure Handling

- Missing or invalid `PUBLIC_BASE_URL` fails before Helm changes the cluster.
- Helm deployment errors remain fail-closed through the existing workflow.
- Post-deploy health verification failure fails the deployment job and prints
  sanitized Kubernetes diagnostics; it must not print credentials or raw
  secret values.
- OAuth verification failure is reported separately with the callback URL
  shape and HTTP status, without logging tokens, cookies, authorization codes,
  or response bodies.

## Verification

Implementation verification must include:

1. Focused shell tests for accepted and rejected `PUBLIC_BASE_URL` values.
2. Deployment safety contract tests covering all four profiles.
3. `helm lint` with the HTTPS base plus remote HTTP overlay.
4. `helm template` assertions proving hostless Ingress, `web`, no TLS,
   `SECURE_COOKIES=false`, and the injected `:6879` URLs.
5. Existing Helm image digest rendering tests.
6. Project risk guardrails, including the deployment and supply-chain checks.
7. Repository-required Go and frontend checks if affected files bring those
   suites into scope.
8. Real remote verification through `http://<public-ip>:6879/`,
   `/api/health`, and a GitHub OAuth login/callback flow using sanitized output.

The implementation is not complete until the public 6879 path, frontend,
backend health endpoint, and OAuth flow have been verified, or an external
OAuth configuration blocker has been explicitly reported.

## Documentation Changes

Update the remote deployment and Helm guides to document:

- the public 6879 to internal 80 forwarding boundary;
- required `PUBLIC_BASE_URL` format and GitHub Environment location;
- the GitHub OAuth callback setting;
- the deliberate HTTP risk and the migration path back to HTTPS;
- verification commands that do not disclose secrets.

Generated architecture documentation is updated only if its documented source
is part of the normal repository workflow; it must not be hand-edited when the
repository treats it as generated output.

## Out Of Scope

- Applying for a domain or TLS certificate.
- Changing the cloud firewall or port-forwarding rule.
- Exposing Kubernetes dependencies or backend ports publicly.
- Broadly allowing HTTP in production profiles.
- Changing authentication protocols or replacing GitHub OAuth.
