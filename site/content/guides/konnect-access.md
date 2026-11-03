---
title: Konnect read-only access
description: Give kuma-migrator read access to a Konnect-hosted Mesh Global control plane with a short-lived system account token, without sharing an admin token.
order: 55
---

`extract` reads a Konnect-hosted Global control plane with a bearer token. The person running
`kuma-migrator` (a platform engineer, or Kong Professional Services) often has no access to the
organization. Do not share an admin personal access token for this. An organization admin
creates a **system account** with read-only access and hands over its token, which expires on
its own.

`kuma-migrator konnect token-script` writes the script that does this. kuma-migrator does not
call Konnect and never sees the admin token: the admin reads the script, runs it, and passes on
the read-only token only.

## Generate the script

```bash
kuma-migrator konnect token-script --region eu -o konnect-readonly-token.sh
```

| Flag | Default | Description |
|---|---|---|
| `--region` | required | Konnect region of the Mesh control planes: `us`, `eu`, `au`, `me`, `in`, `sg` |
| `--scope` | `mesh` | `mesh`: the `Viewer` role on Mesh control planes. `org`: membership of the predefined **Organization Admin (Read Only)** team |
| `--control-plane` | all in the region | Mesh control plane to grant, by name. Repeatable. `--scope mesh` only |
| `--account-name` | `kuma-migrator-readonly` | System account the script creates, or reuses when it exists |
| `--expires-in` | `336h` (14 days) | Token lifetime from the time you generate the script. Between `1h` and `8760h` |
| `-o`, `--output` | stdout | File to write |

Pick the narrowest scope that works:

| Scope | Grants | Use it when |
|---|---|---|
| `mesh` | `Viewer` on each selected Mesh control plane: read and list, nothing outside Mesh | The token is only for `kuma-migrator extract` |
| `org` | Read access to every Konnect resource: gateways, portals, teams, Mesh | The same token also serves a Gateway or platform review |

## Run it (organization admin)

The admin needs `bash`, `curl`, `jq` and a personal access token from an organization admin
account:

```bash
KONNECT_ADMIN_PAT=kpat_... bash konnect-readonly-token.sh
```

The script:

1. creates the system account, or reuses the one with the same name;
2. grants access: `Viewer` on each Mesh control plane (`--scope mesh`), or adds the account to
   **Organization Admin (Read Only)** (`--scope org`);
3. mints one access token with the expiry chosen at generation time and prints it, once;
4. prints the `kumactl` and `extract` commands for each Mesh control plane, and the command that
   revokes access.

It stops at the first failed API call and prints the HTTP status and response. It mints no
token if a control plane named with `--control-plane` does not exist. The admin token goes
only to `global.api.konghq.com` and `<region>.api.konghq.com` and is never printed.

Send the read-only token over a channel you trust, such as a password manager share. Do not
paste it in a ticket or a chat channel.

## Use the token

Register each Mesh control plane in `kumactl`, then extract. The script prints these commands
with the control plane IDs filled in:

```bash
export KONNECT_RO_TOKEN=spat_...
kumactl config control-planes add --name konnect-prod-global \
  --address "https://eu.api.konghq.com/v1/mesh/control-planes/$CONTROL_PLANE_ID/api" \
  --headers "authorization=Bearer $KONNECT_RO_TOKEN"

kuma-migrator extract --kumactl-context konnect-prod-global \
  --output-dir ./raw-policies
```

`extract` sends only `GET` requests. For how it reads Konnect, see [Extract](extract.md#konnect).
For the rest of the run, see the [Konnect walkthrough](getting-started.md#konnect).

## Revoke access

When the extract is done, the admin deletes the system account. This revokes its tokens too:

```bash
curl -X DELETE "https://global.api.konghq.com/v3/system-accounts/$SYSTEM_ACCOUNT_ID" \
  -H "Authorization: Bearer $KONNECT_ADMIN_PAT"
```

The script prints this command with the account ID filled in. Without it, the token stops
working at its expiry date and the account stays in the organization.

## Without the CLI

The script calls three Konnect APIs. To do the same by hand or in Terraform:

| Step | API call |
|---|---|
| Create the account | `POST https://global.api.konghq.com/v3/system-accounts` |
| `--scope mesh` | `POST /v3/system-accounts/<id>/assigned-roles` with `role_name: Viewer`, `entity_type_name: Mesh Control Planes`, `entity_id: <control plane ID>`, `entity_region: <region>` |
| `--scope org` | `POST /v3/teams/<Organization Admin (Read Only) team ID>/system-accounts` with the account ID |
| Mint the token | `POST /v3/system-accounts/<id>/access-tokens` with `name` and `expires_at` |

Konnect documents the roles in
[Teams and roles](https://developer.konghq.com/konnect-platform/teams-and-roles/#mesh-control-planes).
The same system account model gives zones their `Connector` token.
