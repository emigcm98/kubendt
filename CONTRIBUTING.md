# Contributing to KubeNDT

First of all, thank you for your interest in contributing to KubeNDT.

KubeNDT aims to provide a flexible and dynamic platform for network experimentation and orchestration on Kubernetes.

Please read [CLA.md](CLA.md) before opening a pull request.

## Development Setup

Install the following tools locally before starting:

| Tool | Minimum version | Notes |
|------|----------------|-------|
| Go | 1.26 | CGO must be enabled |
| gcc / musl-dev | system default | required by CGO |
| libsqlite3-dev | system default | SQLite C bindings |
| Node.js | 24 LTS | frontend; version pinned in `frontend/.nvmrc` |
| npm | 11+ | bundled with Node |
| kubectl | any recent | cluster access |
| golangci-lint | 2.x | Go linting (backend); install with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` |
| Prettier | 3.x | frontend formatting; installed via `npm install` (devDependency) |

> `go install` places binaries in `$(go env GOPATH)/bin`; make sure that directory is on your `PATH`.

**Backend** (from `backend/`):
```bash
go run .
```
The backend regenerates Swagger docs automatically on startup. If you add or change routes, regenerate manually:
```bash
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g main.go
```

**Frontend** (from `frontend/`):
```bash
nvm use       # optional: selects the Node version from .nvmrc
npm install
npm start
```

The API is authenticated by default. For local development you can either set
`KUBENDT_ADMIN_PASSWORD` (and log in), or run the backend with
`KUBENDT_AUTH_DISABLED=true` to skip auth entirely.

**Production build** (from the repo root):
```bash
docker compose up --build
```

## Development Philosophy

KubeNDT values:

* clean architecture,
* modularity,
* extensibility,
* runtime dynamism,
* and real-world applicability.

Contributions aligned with these goals are welcome.

## Contribution Workflow

The standard way to contribute is via **fork and pull request**:

1. **Fork** the repository on GitHub (button at the top right of the repo page). This creates your own copy of the project under your GitHub account.

2. **Clone** your fork locally:
   ```bash
   git clone https://github.com/<your-username>/kubendt.git
   cd kubendt
   ```

3. **Create a short-lived branch** for your change, named by intent
   (`feat/…`, `fix/…`, `docs/…`):
   ```bash
   git checkout -b feat/my-feature
   ```

4. **Make your changes**, commit, and push to your fork:
   ```bash
   git add .
   git commit -m "Describe what you changed"
   git push origin feat/my-feature
   ```

5. **Open a pull request** from your branch to the `main` branch of the original repository. GitHub will prompt you to do this automatically after pushing. CI (lint, format, build, tests) must pass before the PR can be merged.

6. **Confirm the CLA in the pull request description.** The PR template already
   includes the line:
   ```
   I have read and agree to the KubeNDT CLA (CLA.md).
   ```
   Leave it in place to record your acceptance. Pull requests without this
   confirmation will not be merged.

## Before Contributing

For bug fixes, small improvements, or documentation changes, feel free to open a PR directly.

For larger changes (new features, architectural changes, significant refactors), opening an issue first is appreciated so we can discuss the approach before you invest time implementing it. It is not mandatory, but it helps avoid wasted effort.

## Code Style

* All code and comments must be written in English.
* Prioritize readability over cleverness.
* Keep components modular and reusable.
* Avoid unnecessary abstractions.

### Linting & Formatting

**Backend** (from `backend/`) is linted with golangci-lint (config in `backend/.golangci.yml`):
```bash
golangci-lint run ./...   # report issues
gofmt -w .                # auto-format
```

**Frontend** (from `frontend/`) is formatted with Prettier (config in `frontend/.prettierrc`):
```bash
npm run format            # auto-format src/
npm run format:check      # check without writing
```

### Testing

Both suites also run in CI on every pull request.

**Backend** (from `backend/`), standard Go tests:
```bash
go test ./...             # all packages
go test ./auth/ -v        # a single package, verbose
```
Tests are unit-level and need no cluster: they cover the `auth` package
(sessions, API tokens, login throttle), the driver **capabilities** (command
generation) and pure helpers (name resolution, interface validation). Tests
that touch Kubernetes use the `client-go` fake clientset.

**Frontend** (from `frontend/`), Jest + React Testing Library:
```bash
npm test                        # watch mode
npm test -- --watchAll=false    # single run (as in CI)
```
Component tests (e.g. `Login`, `AuthGate`) mock `fetch`; put a test next to its
source as `*.test.js`.

### Documentation Style

* Keep top-level docs concise and link to deeper technical docs.
* Prefer executable examples over long prose where possible.
* Update Swagger docs when API routes or payloads change.

## Commit Messages

Write commit messages in English, using the imperative mood and no trailing period.

Good examples:

* `Add VyOS interface state synchronization`
* `Fix reconciliation loop on pod restart`
* `Refactor topology update handler`
* `Update OSPF capability docs`

Avoid vague messages like `fix`, `update`, `changes`, or `WIP`.

## Pull Request Checklist

When submitting a pull request:

* Explain the motivation and describe the implementation.
* Mention limitations if any.
* Include screenshots when relevant (UI changes).
* API/behavior changes documented in the PR description.
* Any impacted docs updated (`README.md`, `doc/*`, or example README files).
* Swagger regenerated if routes/contracts changed.
* Scope limited to one logical change.
* Keep the CLA confirmation line from the template (see step 6 above).

## Contributor License Agreement (CLA)

By contributing to KubeNDT, you agree to the terms described in [CLA.md](CLA.md).

In short: you keep copyright over your contributions, but grant the project maintainer the right to relicense contributions as part of KubeNDT. This keeps the project open source while preserving the option for future dual licensing.

Acceptance is recorded by the CLA confirmation line in the pull request description, as described in step 6 of the workflow above.

## Community Expectations

Please:

* be respectful,
* provide constructive feedback,
* focus discussions on technical merit.

Toxic or hostile behavior will not be tolerated. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for the full policy.
