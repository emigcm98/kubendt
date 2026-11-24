# Security Policy

## Supported Versions

KubeNDT is developed on a rolling basis. Security fixes target the **latest released version**; older versions are not maintained.

| Version                | Supported |
| ---------------------- | --------- |
| Latest release (`1.x`) | ✅        |
| Older / unreleased     | ❌        |

## Reporting a Vulnerability

**Please do not open public GitHub issues for security vulnerabilities.**

Report them privately by email to **er.garciadelacalera@gmail.com**. Include:

- a description of the issue and its impact,
- steps to reproduce (or a proof of concept),
- affected version / commit and your environment.

You can expect an initial acknowledgement within a few days. Once a fix is available we will coordinate disclosure. Thank you for reporting responsibly.

## Security Model

Understanding what KubeNDT is helps set expectations:

- **KubeNDT is as privileged as its kubeconfig.** It operates a Kubernetes cluster on your behalf: it can create/delete namespaces, deploy and modify topologies, open interactive shells into pods and mount files. Access to the dashboard or API is therefore effectively **access to the cluster**.
- The kubeconfig is stored in a persistent volume (`/data/.kube/config`) owned by the non-root runtime user. Protect that volume as you would the kubeconfig itself.

### Authentication

- The API and dashboard require authentication. Login uses an account's password (stored as a bcrypt hash) and issues an **HttpOnly, SameSite=Strict session cookie**.
- Accounts have a role (`owner`, `admin` or `user`) and a level in each namespace they reach. Every route carries a permission checked against them. See [doc/USERS.md](doc/USERS.md).
- Sessions are signed cookies that expire after inactivity (`KUBENDT_SESSION_IDLE_HOURS`, default 12h) and an absolute lifetime (`KUBENDT_SESSION_MAX_HOURS`, default 7d). A changed or reset password, or a disabled account, ends them.
- Programmatic access uses **API tokens** (`Authorization: Bearer <token>`), which are revocable and can be given an expiry. A token acts as the account that minted it and cannot be used to mint or revoke other tokens (that requires a session or the password).
- Chosen passwords meet a minimum length (`KUBENDT_PASSWORD_MIN_LENGTH`, default 8).
- Login has in-memory rate limiting per client address and per account (lockout after repeated failures). Behind a reverse proxy the client IP is the proxy's, so the address limit is effectively global unless trusted proxies are configured.

### Hardening already in place

- Both containers run as **non-root**.
- Build/runtime images exclude local artifacts (`.dockerignore`), so a developer's database or binaries are never baked into a published image.

### Operator responsibilities

- **Change the default password.** The example compose files ship `KUBENDT_ADMIN_PASSWORD=admin123` for convenience. Set your own before any real deployment.
- **Use TLS when exposed.** The stock compose serves plain HTTP behind nginx. For anything beyond a trusted local network, terminate TLS in front and set `KUBENDT_COOKIE_SECURE=true`.
- **Run on a trusted network**, or front KubeNDT with an authenticating proxy (e.g. oauth2-proxy) or SSO for stronger, per-user access control.
- `KUBENDT_AUTH_DISABLED=true` turns off authentication entirely. Use it only on a trusted, isolated network.

## Known Limitations

- **Users are trusted people, not tenants.** Access is enforced by KubeNDT's API over one kubeconfig. An account with a shell in a privileged lab node can reach the worker, and labs are not confined from each other on the cluster network. See the trust model in [doc/USERS.md](doc/USERS.md).
- **No single sign-on.** Accounts are KubeNDT's own, there is no identity provider login yet.
- **API tokens carry their account's whole scope.** There are no per-token permission scopes.
- **Login rate limiting is coarse behind a proxy** (see above).
