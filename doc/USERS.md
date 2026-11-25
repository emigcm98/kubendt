# Users and workspaces

KubeNDT has accounts. Each one has a role on the platform and a level in every namespace it can reach, and a namespace belongs to whoever created it. This page says what each of those may do, where it is kept, and where the isolation ends.

## Accounts and roles

| Role | What it is |
|---|---|
| `owner` | The account `admin`, created at first start with `KUBENDT_ADMIN_PASSWORD`. It names and demotes admins and saves the platform's settings, and cannot be disabled, deleted or demoted. |
| `admin` | Has the owner level in every namespace, loads the kubeconfig, sees the cluster and manages the plain users. It never changes the owner or another admin. |
| `user` | Sees only the namespaces it owns or was invited to. It creates namespaces only when its account says so. |

An admin creates accounts from the **Accounts** page or with `POST /users`. A password left empty is generated and shown once. Either way the account must set its own at the first sign-in, and nothing else answers until it does.

A login without a username is the owner's, so scripts written before accounts keep working.

## Levels in a namespace

The owner of a namespace invites other accounts and gives each a level. An admin can do the same from an account's card.

| Level | May |
|---|---|
| `viewer` | See the lab: topology, pods, links, files, history. |
| `operator` | Also work on the pods without changing the lab: shells, captures, traces, throughput tests. |
| `editor` | Also change the lab: deploy, modify, configure, restart, files, layout, history. |
| owner | Also delete the namespace, clear it, share it and hand it over, and open its state browser. |

An operator has a shell, so it can change a node by hand. What it cannot do is change the lab through KubeNDT, so nothing it types is kept or replayed.

Sharing is `PUT /namespaces/access/{namespace}` with the owner and the guests, or the share dialog, from a namespace's row on the Home and from the namespace menu. Handing a namespace over names a new owner, and the old one stays as an editor. A deleted account leaves every guest list, and its namespaces pass to the account the delete names, or to the admins.

## Quotas

An account carries three limits, each `0` for none: the namespaces it may own, the pods across them, and the pods in any one of them. An account without figures of its own takes the defaults of the environment (`KUBENDT_QUOTA_NAMESPACES`, `KUBENDT_QUOTA_PODS`, `KUBENDT_QUOTA_PODS_PER_NAMESPACE`). Admins are never counted.

A lab counts against its owner, whoever deploys into it. The check runs before anything is created, on a new namespace, a deploy, a modify and a scale, and answers `403` with the code `QUOTA_EXCEEDED`.

With `KUBENDT_POD_CPU_REQUEST`, `KUBENDT_POD_MEMORY_REQUEST`, `KUBENDT_POD_CPU_LIMIT` and `KUBENDT_POD_MEMORY_LIMIT` set, every new lab namespace gets a LimitRange with those defaults, so the scheduler counts the pods and one lab cannot starve a worker.

## Passwords and sign-in

- A chosen password has at least eight characters (`KUBENDT_PASSWORD_MIN_LENGTH`), is not the account's name and, on a change, is not the current one. A refusal is `400` with the code `WEAK_PASSWORD`.
- Failed sign-ins are counted per client address and per account. After 8 from one address or 12 on one account within 15 minutes, further attempts get `429` with `Retry-After`. One address alone never locks an account out. API tokens are not sign-ins and keep working.
- Changing or resetting a password ends the account's sessions. So does disabling it, which also stops its tokens.
- A session is a signed cookie that holds for the idle and absolute lifetimes of [DEPLOYMENT.md](DEPLOYMENT.md#authentication). The role is read from the account at every request, so a change applies within seconds.

## API tokens

Each account mints and revokes its tokens and changes its password on its profile page, `/profile`. A token acts as the account that minted it and never above it. It is minted with a session or the password, never with another token. The owner sees and revokes everyone's tokens, an admin those of the plain users, each account its own. Tokens minted before accounts existed are the owner's.

## Where it is kept

Everything is an object of the cluster, readable with `kubectl` and shown by the state browser.

| What | Object |
|---|---|
| An account | Secret `kubendt-user-<name>` in the system namespace: password hash, role, standing, flags, quota |
| An API token | Secret `kubendt-token-<id>` in the system namespace, with its account |
| A namespace's owner and guests | Annotations `kubendt/owner` and `kubendt/access` on the Namespace |

## The trust model

Access is enforced by KubeNDT's API, and KubeNDT talks to the cluster with one kubeconfig. That sets the limits:

- **Users are trusted people, not tenants.** Lab nodes that route or bridge run privileged. An operator or an editor has a root shell in them, which a determined person can turn into access to the worker. Give accounts to colleagues and students you would give a lab bench to.
- **Labs are not confined from each other on the network.** A pod reaches other namespaces over the cluster network unless the cluster's CNI enforces NetworkPolicies and you write them.
- **Anyone with the kubeconfig is outside all of this.** `kubectl` sees and changes every lab, account and token.
- **Every changing request is logged** by the backend with the account, its level, the method and the path.

Signing in through an identity provider is not there yet. The accounts are KubeNDT's own.

## Two users, one lab

Alice has an account that may create namespaces. Bob has a plain one.

1. The admin opens **Accounts**, creates `alice` with "May create namespaces" and `bob` without, and passes each the password shown.
2. Alice signs in, sets her password, creates the namespace `ospf-lab` and deploys a topology into it. Bob signs in and sees no namespaces.
3. Alice opens the namespace menu, picks **Share or hand over** and invites `bob` as a `viewer`. Bob now sees `ospf-lab` with its level, opens it, and finds the toolbar disabled.
4. Alice raises Bob to `operator`. He opens shells, captures on a link and runs a trace, but a configure is refused.
5. Alice raises Bob to `editor`. He applies a configuration and restarts a router. Deleting the namespace is still Alice's.

The same from the API, as Alice:

```bash
curl -b cookies.txt -X PUT http://localhost/api/namespaces/access/ospf-lab \
  -H 'Content-Type: application/json' \
  -d '{"owner": "alice", "guests": {"bob": "editor"}}'
```
