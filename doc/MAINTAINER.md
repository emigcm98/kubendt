# Maintainer Guide

This document describes the branching model used in KubeNDT and how external contributions are integrated.

## Branch Model

KubeNDT follows a simple trunk-based model (GitHub Flow):

| Branch | Purpose |
|--------|---------|
| `main` | Always stable and deployable. This is what users see and clone. It is protected: no direct pushes, changes land only through reviewed pull requests with green CI. |
| short-lived branches | One branch per change, branched off `main` and deleted after merge. |

Name working branches by intent, e.g. `feat/ospf-redistribution`, `fix/namespace-409`, `docs/vyos-example`. There is no long-lived integration branch.

Releases are tagged on `main` using semantic versioning. Git tags carry the leading `v` (`v1.0.0`, `v1.1.0`, …); the published container images drop it and use plain semver (`1.0.0`), see [Releasing a New Version](#releasing-a-new-version).

## Handling External Contributions

Contributors open pull requests from their fork targeting `main` (as documented in [CONTRIBUTING.md](../CONTRIBUTING.md)). They cannot push to the repository directly, so every external change arrives as a PR.

Your review checklist:
- [ ] CLA confirmation is present in the PR description
- [ ] Code follows project style
- [ ] No unrelated changes bundled in
- [ ] CI is green (lint, format, build, tests)
- [ ] Docs updated if the change affects them

Once approved and CI is green, merge the PR into `main` and delete the branch.

## Releasing a New Version

`main` is always releasable, so a release is just a tag on top of it:

```bash
git checkout main
git pull
git tag v1.2.0
git push origin v1.2.0
```

Pushing the tag triggers `.github/workflows/release.yml`, which builds and publishes the backend and frontend images to GHCR. The workflow strips the leading `v`, so tag `v1.2.0` publishes `ghcr.io/emigcm98/kubendt-backend:1.2.0` (and moves `:latest`).

Then create the GitHub release from the tag with a short changelog.

### If a tag points at the wrong commit

A tag is just a movable pointer. Before anyone depends on it you can delete and recreate it:

```bash
git push origin :refs/tags/v1.2.0   # delete the remote tag
git tag -d v1.2.0                    # delete it locally
git tag v1.2.0 <correct-commit>      # recreate
git push origin v1.2.0
```

If a GitHub release or published images already exist for that tag, remove/redo them too, they are not cleaned up automatically.

## Keeping `main` Clean

- Never commit directly to `main`; use a branch and a pull request.
- Rely on branch protection to require green CI before merge.
- `main` should always be in a state that someone can clone and deploy.
