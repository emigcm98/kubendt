# Deployment and Configuration

## Running Locally (Development)

Install the required tools listed in [CONTRIBUTING.md](../CONTRIBUTING.md), then run each component in a separate terminal.

**Backend** (from `backend/`):

```bash
go run .
```

Runs on http://localhost:8080. Requires Go 1.26+ with CGO, gcc, and libsqlite3-dev.

**Frontend** (from `frontend/`):

```bash
npm ci
npm start
```

Runs on http://localhost:3000.

The frontend dev server proxies `/api` requests to the backend at `localhost:8080`.

The backend reads configuration from environment variables at startup, there is no auto-loaded `.env` file. Set any needed variables in your shell before running:

```bash
export CORS_ALLOWED_ORIGINS=http://localhost:3000 # default when unset
export FILES_BASE_PATH=./files # default when unset
export KUBENDT_DB_PATH=./kubendt.db # default when unset: kubendt.db in cwd
go run .
```

Or create a local file and source it (not committed to git):

```bash
# backend/.env.local , not committed
CORS_ALLOWED_ORIGINS=http://localhost:3000
FILES_BASE_PATH=./files
```

```bash
set -a && source backend/.env.local && set +a
```

## Deployment with Docker Compose

Two compose files are provided, both running the backend plus a combined nginx+frontend image:

- `docker-compose.prod.yml`: pulls the published images from GHCR (nothing to build). The "just use it" path.
  ```bash
  docker compose -f docker-compose.prod.yml up -d
  ```
- `docker-compose.yml`: builds both images from source (for local changes).
  ```bash
  docker compose up --build
  ```

Configuration lives **inline** in each compose file's `environment:` block. The defaults expect your kubeconfig at `~/.kube/config` (mounted read-only) and set a placeholder admin password (`admin123`) that you should change. See the reference below for every variable.

File storage and the SQLite database are persisted in named Docker volumes (`kubendt-files` and `backend-db`). They survive container restarts and rebuilds.

## Environment Variables Reference

### Backend

- `KUBECONFIG`: kubeconfig path.
- `KUBE_CONTEXT`: optional kube context override.
- `PORT`: API port (default `8080`).
- `CORS_ALLOWED_ORIGINS`: comma-separated list of allowed frontend origins (default: `http://localhost:3000`).
- `FILES_BASE_PATH`: namespace files base folder (default: `files` relative to cwd).
- `KUBENDT_DB_PATH`: SQLite database path (default: `kubendt.db` in cwd).
- `KUBECTL_EXEC_TIMEOUT_SECONDS`: max wall-clock seconds for any single in-pod exec (kubectl exec, `ssh_qemu` into a QEMU guest, batched VyOS commits, etc.). Default `30`. Bump it if you run against a slow cluster or busy guests and start seeing "exec timeout" errors from network-configure or modify operations. The SSH layer detects dead sessions independently within ~11s, so this deadline only bounds genuinely-long commands.
- `SWAGGER_BASE_PATH`: base path prefix for Swagger UI (e.g. `/api/` when served behind a proxy).
- `KUBENDT_VERSION`: optional build version metadata.
- `KUBENDT_COMMIT`: optional build commit metadata.
- `KUBENDT_BUILD_DATE`: optional build date metadata.

#### Authentication

- `KUBENDT_ADMIN_PASSWORD`: admin password for login. If unset, a random one is generated on first run and printed to the logs once (its bcrypt hash is then stored, so it stays stable across restarts).
- `KUBENDT_AUTH_DISABLED`: set to `true` to run the API **unauthenticated** (dev / trusted network only). A loud warning is logged at startup.
- `KUBENDT_COOKIE_SECURE`: set to `true` when serving over HTTPS so the session cookie is only sent over TLS (default `false`, since the stock compose serves plain HTTP behind nginx).
- `KUBENDT_SESSION_IDLE_HOURS`: idle timeout for browser sessions (default `12`).
- `KUBENDT_SESSION_MAX_HOURS`: absolute session lifetime (default `168` = 7 days).

Programmatic access uses **API tokens** (created in the dashboard or via `POST /auth/tokens` with the admin password, optionally with an expiry via `expires_in_days`), sent as `Authorization: Bearer <token>`.

### Frontend

- `REACT_APP_API_BASE_URL`: API base URL.
- `PORT`: frontend dev server port (default `3000`).
