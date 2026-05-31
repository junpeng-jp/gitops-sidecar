# GitOps Sidecar

The GitOps sidecar clones configured git repositories on startup and exposes a REST API to query sync state and trigger pulls.

---

## Configure the sidecar

Configuration has two parts: a JSON config file and environment variables.

### Environment variables

| Variable             | Required | Default                   | Description                  |
|----------------------|----------|---------------------------|------------------------------|
| `GITOPS_PORT`        | no       | `9001`                    | HTTP listen port             |
| `GITOPS_CONFIG_FILE` | no       | `/etc/gitops/config.json` | Path to the JSON config file |

### Config file

Mount the config file as a Kubernetes ConfigMap at `/etc/gitops/config.json`, or override the path with `GITOPS_CONFIG_FILE`.

```json
{
  "runtimeDir":   "/run/gitops-runtime",
  "workDir":      "/tmp/gitops",
  "workspaceDir": "/config/.gitops/repos",
  "repos": [
    {
      "name":             "ha-config",
      "url":              "git@github.com:org/ha-config.git",
      "verifyCommit":     true,
      "commandQueueSize": 16
    }
  ],
  "notification": {
    "type":          "ha-webhook",
    "url":           "http://app.example.com/api/webhook/<id>",
    "queueSize":     64,
    "maxBatchSize":  16,
    "batchInterval": "3s"
  }
}
```

| Field          | Required | Default       | Description                                              |
|----------------|----------|---------------|----------------------------------------------------------|
| `workspaceDir` | yes      | —             | Final destination for checked-out repo content           |
| `repos`        | yes      | —             | At least one repo entry required                         |
| `runtimeDir`   | no       | —             | Pre-populated by init container; see SSH setup below. Omit to skip SSH env var setup entirely. |
| `workDir`      | no       | `/tmp/gitops` | Ephemeral scratch space; wiped on every startup          |
| `notification` | no       | —             | Omit to disable HA webhook notifications                 |

Each entry in `repos` supports these fields:

| Field                    | Required | Default | Description                                                   |
|--------------------------|----------|---------|---------------------------------------------------------------|
| `name`                   | yes      | —       | Repo identifier; must match `^[a-z][a-z0-9-]*$`, max 64 chars |
| `url`                    | yes      | —       | Git remote URL (SSH or HTTPS)                                 |
| `verifyCommit`           | no       | `false` | Run `git verify-commit HEAD` after each pull                  |
| `commandQueueSize`       | no       | `16`    | Depth of the per-repo operation queue                         |

When `notification` is present, these fields are available:

| Field                        | Required | Default | Description                                                      |
|------------------------------|----------|---------|------------------------------------------------------------------|
| `notification.type`          | yes      | —       | Must be `"ha-webhook"`                                           |
| `notification.url`           | yes      | —       | URL to POST event batches to                                     |
| `notification.queueSize`     | no       | `64`    | In-memory event buffer before delivery                           |
| `notification.maxBatchSize`  | no       | `16`    | Flush immediately when this many events are pending              |
| `notification.batchInterval` | no       | `"3s"`  | Send accumulated events on this interval; accepts Go duration strings (`"1s"`, `"500ms"`) |

Repo names must match `^[a-z][a-z0-9-]*$` and be at most 64 characters.

After a successful pull, the sidecar writes files to two directories:

```
<workDir>/
  <name>/.bare/          ← bare clone (git objects)

<workspaceDir>/
  <name>/                ← checked-out repo content
```

---

## Set up SSH access

The sidecar does not manage SSH keys or known hosts. An init container must write the following files before the sidecar starts:

```
<runtimeDir>/
  gitconfig              ← global git config (GIT_CONFIG_GLOBAL)
  ssh_config             ← SSH client config (referenced by GIT_SSH_COMMAND)
  known_hosts            ← referenced inside ssh_config
  allowed-signers/
    <repo-name>          ← per-repo; required only when verifyCommit: true
```

When `runtimeDir` is set, the sidecar validates that required files exist before starting. If any are missing, the sidecar exits immediately. `gitconfig` is always required. `ssh_config` and `known_hosts` are only required when at least one repo URL uses the SSH protocol (`git@` or `ssh://`). When `runtimeDir` is set and valid, the sidecar sets these environment variables so all git subprocesses inherit them:

```
GIT_CONFIG_GLOBAL = <runtimeDir>/gitconfig
GIT_SSH_COMMAND   = ssh -F '<runtimeDir>/ssh_config'
```

If you omit `runtimeDir` from the config, the sidecar skips this setup entirely. Git will use whatever SSH configuration exists in the default locations on the host.

### Minimal `ssh_config` example

```
Host github.com
  IdentityFile /run/gitops-runtime/id_ed25519
  UserKnownHostsFile /run/gitops-runtime/known_hosts
  StrictHostKeyChecking yes
```

### Enable commit verification

When you set `verifyCommit: true` on a repo, the sidecar runs `git verify-commit HEAD` after each pull. This requires a GPG or SSH signing key and a populated `allowed-signers/<repo-name>` file.

Add the following to `gitconfig` to point git at the correct allowed-signers file:

```ini
[gpg "ssh"]
  allowedSignersFile = /run/gitops-runtime/allowed-signers/<repo-name>
```

---

## HTTP API

### Endpoints

`GET /health` is the liveness probe. Returns:

```json
{ "status": "ok", "version": "v1.2.3", "commit": "abc1234", "date": "2024-01-15" }
```

`GET /repos` returns a list of configured repos. Accepts an optional `limit` query parameter (default `10`, max `100`). Results are sorted alphabetically by name.

```json
{
  "repos": [
    {
      "name": "ha-config",
      "state": "ready",
      "ref": "main",
      "path": "/config/.gitops/repos/ha-config",
      "last_updated_at": "2024-01-15T10:30:00Z"
    }
  ]
}
```

The `error` field appears on a repo entry only when `state` is `error`.

`GET /repos/{name}` returns the state for a single repo. It returns `404` if the name is not configured.

```json
{
  "repo": {
    "name": "ha-config",
    "state": "ready",
    "ref": "main",
    "path": "/config/.gitops/repos/ha-config",
    "last_updated_at": "2024-01-15T10:30:00Z"
  }
}
```

`POST /repos/{name}/operation` enqueues an operation for one repo. The request body is a JSON object with a `kind` field. It returns `202` with the repo's current state after the operation is scheduled:

```json
{
  "repo": {
    "name": "ha-config",
    "state": "syncing",
    "ref": "main",
    "path": "/config/.gitops/repos/ha-config",
    "last_updated_at": "2024-01-15T10:30:00Z"
  }
}
```

The only supported operation is `pull`, which requires a `ref` field:

```json
{ "kind": "pull", "ref": "main" }
```

`POST /reset` wipes both `workDir` and `workspaceDir`, then re-runs bare clones for all repos. It returns `202` on success:

```json
{ "success": true }
```

On failure it returns an error status with:

```json
{ "error": "..." }
```

### Repo states

| State      | Meaning                                                           |
|------------|-------------------------------------------------------------------|
| `init`     | Bare clone in progress (set at startup or after reset)            |
| `syncing`  | Fetch and worktree update in progress                             |
| `ready`    | Worktree is current; `last_updated_at` is set                     |
| `error`    | Last operation failed; `error` field contains the failure message |

### Error responses

`400 Bad Request` on `POST /repos/{name}/operation` means the request body is invalid. Check that `kind` is `"pull"` and that `ref` is non-empty.

`404 Not Found` means the repo name in the request path is not in the config. Check the name against your config file.

`409 Conflict` on `POST /repos/{name}/operation` means the repo is not ready to accept operations. This happens when the bare clone is still in progress (`init` state) or the bare directory does not exist. Wait for the repo to leave `init` state and retry.

---

## Notification webhook contract

When the `notification` key is present in the config, the sidecar POSTs a JSON batch to the configured URL on every repo state transition. Events accumulate in memory and are delivered asynchronously — they may arrive slightly after the HTTP API reflects the new state.

### Payload shape

Each POST delivers a batch of one or more `repo_changed` events. The body is always a JSON object with an `updates` array:

```json
{
  "updates": [
    {
      "event_kind":      "repo_changed",
      "name":            "ha-config",
      "state":           "ready",
      "previous_state":  "syncing",
      "ref":             "main",
      "last_updated_at": "2024-01-15T10:30:00Z"
    }
  ]
}
```

Error state example inside `updates`:

```json
{
  "event_kind":     "repo_changed",
  "name":           "ha-config",
  "state":          "error",
  "previous_state": "syncing",
  "ref":            "main",
  "error":          "exit status 128: ..."
}
```

### Event fields

| Field            | Type            | Always present | Description                                                     |
|------------------|-----------------|----------------|-----------------------------------------------------------------|
| `event_kind`     | string          | yes            | Always `"repo_changed"`                                         |
| `name`           | string          | yes            | Repo name as configured                                         |
| `state`          | string          | yes            | New state: `init`, `syncing`, `ready`, or `error`               |
| `previous_state` | string          | yes            | State before this transition                                    |
| `ref`            | string          | yes            | Git ref currently checked out; empty before the first successful pull or after reset |
| `last_updated_at`| string (RFC3339)| yes (nullable) | Timestamp of the last successful pull; `null` until first success |
| `error`          | string          | no             | Present only when `state` is `"error"`; contains the failure message |

### Delivery guarantees

- Each POST carries a batch of one or more events as `{ "updates": [...] }`
- The sidecar flushes accumulated events every `batchInterval` (default `3s`) or immediately when `maxBatchSize` events are pending (default `16`)
- The sidecar does not retry on delivery failures or non-2xx responses
- A 10-second timeout applies to each POST; failures are logged but do not affect sidecar operation
- Events for a single repo are delivered in the order transitions occur
- `POST /reset` fires one `repo_changed` event per configured repo, with `state: "init"` and `ref: ""`
