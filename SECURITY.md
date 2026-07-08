# Security Policy

## Supported Versions

KubeNDT is developed on a rolling basis. Security fixes target the **latest
released version**; older versions are not maintained.

| Version | Supported |
|---------|-----------|
| Latest release (`1.x`) | ✅ |
| Older / unreleased | ❌ |

## Reporting a Vulnerability

**Please do not open public GitHub issues for security vulnerabilities.**

Report them privately by email to **er.garciadelacalera@gmail.com**. Include:

- a description of the issue and its impact,
- steps to reproduce (or a proof of concept),
- affected version / commit and your environment.

You can expect an initial acknowledgement within a few days. Once a fix is
available we will coordinate disclosure. Thank you for reporting responsibly.

## Security Model

Understanding what KubeNDT is helps set expectations:

- **KubeNDT is as privileged as its kubeconfig.** It operates a Kubernetes
  cluster on your behalf: it can create/delete namespaces, deploy and modify
  topologies, open interactive shells into pods and mount files. Access to the
  dashboard or API is therefore effectively **access to the cluster**.
- The kubeconfig is stored in a persistent volume (`/data/.kube/config`) owned
  by the non-root runtime user. Protect that volume as you would the
  kubeconfig itself.

### Authentication

- The API and dashboard require authentication. Login uses a password (stored
  as a bcrypt hash) and issues an **HttpOnly, SameSite=Strict session cookie**.
- Sessions expire after inactivity (`KUBENDT_SESSION_IDLE_HOURS`, default 12h)
  and an absolute lifetime (`KUBENDT_SESSION_MAX_HOURS`, default 7d), and are
  **cleared whenever the backend restarts**, so users must sign in again.
- Programmatic access uses **API tokens** (`Authorization: Bearer <token>`),
  which are revocable and can be given an expiry. Tokens carry admin scope and
  cannot be used to mint or revoke other tokens (that requires a session or the
  password).
- Login has basic in-memory rate limiting (lockout after repeated failures).
  Behind a reverse proxy the client IP is the proxy's, so the limit is
  effectively global unless trusted proxies are configured.

### Hardening already in place

- Both containers run as **non-root**.
- Build/runtime images exclude local artifacts (`.dockerignore`), so a
  developer's database or binaries are never baked into a published image.

### Operator responsibilities

- **Change the default password.** The example compose files ship
  `KUBENDT_ADMIN_PASSWORD=admin123` for convenience. Set your own before any
  real deployment.
- **Use TLS when exposed.** The stock compose serves plain HTTP behind nginx.
  For anything beyond a trusted local network, terminate TLS in front and set
  `KUBENDT_COOKIE_SECURE=true`.
- **Run on a trusted network**, or front KubeNDT with an authenticating proxy
  (e.g. oauth2-proxy) or SSO for stronger, per-user access control.
- `KUBENDT_AUTH_DISABLED=true` turns off authentication entirely. Use it only on
  a trusted, isolated network.

## Known Limitations

- **Single admin account.** There is one admin identity with full access. There
  is no multi-user support or fine-grained RBAC yet (the session model already
  carries identity/roles to make this an additive change later).
- **API tokens are admin-scoped.** There are no per-token permission scopes.
- **Login rate limiting is coarse behind a proxy** (see above).
