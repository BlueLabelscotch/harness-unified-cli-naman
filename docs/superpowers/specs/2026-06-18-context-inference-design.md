# Context Inference Design

**Date:** 2026-06-18
**Status:** Revised (post Opus review)

## Problem

Two related friction points in the CLI today:

1. Commands like `harness list pr`, `harness list branch`, `harness list commit` require a `<repo_id>` positional argument even when the user is already inside a cloned Harness Code repository. There is no way to default it from the environment.

2. When a resource exists in a different org/project than the user's profile default, the API returns a 404 with no helpful guidance. Users must discover and pass `--org`/`--project` manually.

## Solution Overview

Introduce **local context inference**: before erroring on a missing required positional arg, the CLI resolves a `LocalContext` from the user's environment. It also uses that context to fill in `org`/`project` in the auth resolution chain, eliminating cross-project 404s for users working in cloned repos.

---

## LocalContext

A new package `pkg/gitcontext` owns context resolution. It exposes one function:

```go
func Resolve() (*LocalContext, error)
```

The result type:

```go
type LocalContext struct {
    AccountID string            // feeds into ResolvedAuth
    Org       string            // feeds into ResolvedAuth
    Project   string            // feeds into ResolvedAuth
    Extra     map[string]string // noun-specific values: "repo", "pipeline", etc.
}
```

`AccountID`, `Org`, `Project` are named because they are first-class auth concepts used universally. Noun-specific positional args (`repo`, `pipeline`, `environment`, etc.) live in `Extra` and are looked up by the spec-declared `context_key`.

`Resolve()` never returns an error for "not found" — it returns `&LocalContext{}` with all zero values and `nil` error. A real error (e.g. malformed YAML in context file) is returned so callers can surface it.

### Resolution Chain

Sources are tried in order; the first that produces any populated field wins. A source either contributes all its fields or is skipped entirely — no partial merging between sources.

#### 1. Git Remote URL

Runs `git remote get-url <name>` for each remote name in priority order: `origin`, `upstream`, then all others in alphabetical order. The first URL that matches the Harness Code pattern is used; non-matching URLs are skipped silently.

**HTTPS URL pattern (only HTTPS is supported; SSH URLs are ignored):**
```
https://<host>/code/account/<org>/<project>/<repo>.git
```

Example:
```
https://app.harness.io/code/account/myorg/myproject/my-repo.git
```

Populates: `Org` (`myorg`), `Project` (`myproject`), `AccountID` (derived from the token via `auth.AccountIDFromToken`, not from the URL path — the URL does not contain an account segment in this pattern), `Extra["repo"]` (`my-repo`).

> **Note:** The exact clone URL format must be verified against a real Harness Code `git_url` field value before implementation. The regex used in `gitcontext.go` must be documented and tested with actual clone URLs.

If `git` is not installed or the CWD is not inside a git repo, `git remote get-url` will fail — this is treated as "no match", not an error.

`HARNESS_NO_CONTEXT=1` disables git remote lookup (and context file lookup) entirely. See Escape Hatch below.

#### 2. Context File

Walks up from the current working directory looking for `.harness/context.yaml`. The first file found (closest to CWD) wins.

Format:
```yaml
org: my-org
project: my-project
repo: my-repo
pipeline: my-pipeline   # any extra key is valid
```

Special keys `org`, `project`, `account_id` populate the named struct fields. All other keys populate `Extra`.

`.harness/context.yaml` is **local-only by default** — it should be added to `.gitignore` unless the team explicitly wants to share it. The CLI does not auto-gitignore it; this is a user/team decision.

#### 3. No Context

Returns `&LocalContext{}`. Inference does not happen; existing behavior is preserved.

---

## Spec Change: `context_key` on NounDef

One new optional field on `NounDef` in `pkg/spec/spec.go`:

```go
type NounDef struct {
    // existing fields ...
    ContextKey string `yaml:"context_key,omitempty"`
}
```

Declared in `code.spec.yaml` on nouns whose commands take a required positional arg that can be inferred:

```yaml
nouns:
  - noun: pr
    context_key: repo

  - noun: branch
    context_key: repo

  - noun: commit
    context_key: repo

  - noun: tag
    context_key: repo

  - noun: pr_comment
    context_key: repo

  - noun: pr_activity
    context_key: repo
```

`context_key` is a key into `LocalContext.Extra`. The registry looks up `Extra[noun.ContextKey]` when the required positional arg is absent. This is spec-driven and noun-scoped — no per-command YAML, no hardcoded noun names in Go code.

---

## Auth Resolution Change

### New call sequence in `buildCtx`

Today `buildCtx` calls `auth.Resolve(profileFlag)` which runs `Load()` + `Validate()` in one shot. `Validate()` hard-errors if `OrgID` or `ProjectID` are empty — *before* flag overrides are applied. This makes it impossible to inject `LocalContext` values into auth.

The new sequence in `buildCtx`:

```go
lc, lcErr := gitcontext.Resolve()
// lcErr is only non-nil for malformed context files; "not found" is not an error.
if lcErr != nil {
    return nil, fmt.Errorf("reading local context: %w", lcErr)
}

resolved, err := auth.Load(profileFlag)  // Load only — no validation yet
if err != nil {
    return nil, err
}

// Apply overrides in precedence order: flags > env vars > LocalContext > profile default.
// auth.Load() already applied env vars and profile; flags override that.
// LocalContext fills gaps not covered by flags or env vars.
resolved.OrgID = firstNonEmpty(orgFlag, resolved.OrgID, lc.Org)
resolved.ProjectID = firstNonEmpty(projectFlag, resolved.ProjectID, lc.Project)
// AccountID: LocalContext does NOT override resolved.AccountID — auth.Validate() checks
// that AccountID matches the token; overriding it here could cause a mismatch error.
// LocalContext.AccountID is only used as a sanity-check signal (not wired into auth).

if err := auth.Validate(resolved); err != nil {
    return nil, err
}
```

### Precedence order for Org/Project

1. Explicit `--org` / `--project` flags
2. `HARNESS_ORG` / `HARNESS_PROJECT` env vars  *(applied inside `auth.Load()`)*
3. **LocalContext** `Org` / `Project` *(new — fills gaps after env vars)*
4. Profile defaults  *(applied inside `auth.Load()`)*

**Rationale for env vars above LocalContext:** CI environments use env vars as canonical configuration. A `HARNESS_ORG` set in a pipeline must not be overridden by a `.git/config` in the workspace. LocalContext is a developer-convenience fallback, not an authority.

The same change applies to `buildCompletionCtx` so tab completion also benefits from LocalContext.

---

## Missing Positional Arg Inference

### Simple `requires_parentid` commands (`list pr`, `list branch`, etc.)

When `RequiresParentId` is true, no arg was provided, and the noun has a `context_key`:

```go
if len(args) == 0 && cs.RequiresParentId {
    nd := r.GetNoun(cs.Noun)
    if nd != nil && nd.ContextKey != "" && lc != nil {
        if val := lc.Extra[nd.ContextKey]; val != "" {
            ctx.ParentId = val
            if ctx.IsPty {
                fmt.Fprintf(os.Stderr, "# using %s: %s\n", nd.ContextKey, val)
            }
        }
    }
    if ctx.ParentId == "" {
        return nil, fmt.Errorf("%s %s requires a positional %s argument", ...)
    }
}
```

### `id_parts` commands (`get pr`, `update pr_comment`, etc.)

Some commands use `id_parts > 1` where the first part is the repo (e.g. `get pr <repo>/<pr_number>`). When `context_key` is set on the noun and the user provides an arg with one fewer `/` than expected, the inferred value is prepended.

Rule: if `cs.IdParts > 1` and `nd.ContextKey != ""` and `strings.Count(rawId, "/") == cs.IdParts - 2` (one short), prepend `Extra[nd.ContextKey] + "/"` to the raw id before splitting.

Example:
- `get pr 123` → repo inferred → treated as `my-repo/123` → `IdParts[0]="my-repo"`, `IdParts[1]="123"`
- `update pr_comment 5/22` → repo inferred → treated as `my-repo/5/22` → three parts as expected

If the arg already has the expected number of slashes, no inference occurs — the explicit value wins.

### `id_parts` `requires_parentid` commands (`list pr_activity`, `list pr_comment`)

These nouns have `parentid_label: "<repo_id>/<pr_number>"` and `id_parts: 2`. Here the `parentid` is itself a two-part value. Inference applies only to the repo portion: if the user provides a single bare value (no slash), it is treated as the PR number and the repo is prepended from context.

---

## 404 Friendliness

When the HTTP client receives a 404 and `LocalContext` was empty (i.e., inference did not provide org/project), **and** the request URL contains `/orgs/` or `/projects/` path segments (indicating it was a scoped resource lookup), append to the error:

```
hint: if the resource is in a different org or project, try --org/--project
      or add a .harness/context.yaml in your project directory
```

This scoping check prevents the hint from firing on unscoped endpoints (e.g. account-level resources) where org/project are not relevant.

---

## Escape Hatch

`HARNESS_NO_CONTEXT=1` disables all context inference for the invocation: `gitcontext.Resolve()` returns `&LocalContext{}` immediately without running git or reading the context file. Useful for CI pipelines that want fully explicit configuration.

---

## Testing Strategy

`pkg/gitcontext` must be testable without shelling out to git. The package will define a `GitRunner` interface:

```go
type GitRunner interface {
    RemoteURL(name string) (string, error)
    ListRemotes() ([]string, error)
}
```

The default implementation uses `os/exec`. Tests inject a `MockGitRunner`. `buildCtx` tests use `HARNESS_NO_CONTEXT=1` to isolate from the caller's git environment.

---

## User-Facing Behavior

| Scenario | Before | After |
|---|---|---|
| `harness list pr` inside a cloned Harness Code repo | Error: missing `<repo_id>` | Works; infers repo from git remote, prints hint in TTY |
| `harness list pr` with `.harness/context.yaml` | Error: missing `<repo_id>` | Works; infers repo from context file |
| `harness get pr 123` (no repo prefix) inside clone | Error: expected 2 id parts | Works; prepends repo from git remote |
| `harness list branch` / `list commit` / `list tag` | Error: missing `<repo_id>` | Same inference — all nouns with `context_key: repo` benefit |
| Cross-project repo, profile has wrong org/project | 404 not found | Git remote fills correct org+project automatically |
| `HARNESS_ORG=team-a` set, but git remote has org=team-b | N/A (today) | Env var wins — `team-a` is used |
| Non-Harness remote (GitHub), no context file | N/A | No inference; existing behavior unchanged |
| 404 with no context, scoped request | `404 not found` | `404 not found` + hint |
| `HARNESS_NO_CONTEXT=1` | N/A | Inference fully disabled |

---

## Files Changed

| File | Change |
|---|---|
| `pkg/gitcontext/gitcontext.go` | New package: `Resolve()`, `LocalContext`, `GitRunner` interface |
| `pkg/gitcontext/gitcontext_test.go` | Unit tests: URL parsing, file walking, mock git runner |
| `pkg/spec/spec.go` | Add `ContextKey string` to `NounDef` |
| `pkg/spec/code.spec.yaml` | Add `context_key: repo` to `pr`, `branch`, `commit`, `tag`, `pr_comment`, `pr_activity` nouns |
| `pkg/registry/buildctx.go` | Split `auth.Resolve` → `auth.Load` + `auth.Validate`; apply LocalContext; infer missing positional args; update `buildCompletionCtx` |
| `pkg/client/client.go` | Append scoped 404 hint when inference was not active |
| `pkg/hbase/hbase.go` | Add `EnvNoContext = "HARNESS_NO_CONTEXT"` constant |

---

## Out of Scope

- `context_key` for non-code nouns (pipeline, environment, service) — format is forward-compatible; wiring is a follow-on
- `harness context set` / `harness context status` commands — useful for visibility but not required
- SSH git URL parsing — only HTTPS is supported in this iteration
- Auto-gitignoring `.harness/context.yaml` — left to the user/team
