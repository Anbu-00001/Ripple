# Faultline — AI Catalog Layer

A thin, **platform-native** companion to Faultline, published to the GitLab AI
Catalog as a **declarative GitLab Duo agent**.

## What this layer is

Faultline's real compute is a **Rust engine** (`engine/`) + **Go agent**
(`agent/`) that run in CI: the Go agent pulls a bounded code subgraph from
GitLab Orbit, and the Rust engine computes the *full transitive closure* of a
change's "blast radius" (Orbit's query DSL is capped at 3 hops, so we close it
ourselves).

This catalog layer is **separate from that compute**. It is a declarative,
server-side ("ambient") Duo agent that, when **mentioned or assigned on a merge
request**, reads the MR diff, traces changed symbols to their callers via
repository reads, reasons about transitive downstream and untested impact, and
posts a concise Markdown verdict as an MR note.

> Declarative / ambient catalog agents **cannot run our binary**. They use only
> the platform's built-in tools (diff/file/grep/note). This layer is therefore
> a lighter, dependency-free *estimate* of impact and an integration story — the
> precise, unbounded closure still comes from the Faultline engine in CI.

## How it relates to the CI engine

| | CI engine (`engine/` + `agent/`) | AI Catalog layer (this) |
|---|---|---|
| Runs where | demo project CI pipeline | server-side, ambient |
| Trigger | pipeline | MR mention / MR event |
| Compute | Rust closure over Orbit graph | LLM + built-in repo tools |
| Output | exact transitive blast radius | concise MR note estimate |
| Runs our binary? | yes | no (declarative only) |

The two are complementary: the catalog agent is the always-on, in-platform
front door; the CI engine is the precise backend.

## Files

- `agents/faultline-impact-reviewer.yml` — the declarative Duo agent (primary
  deliverable; schema-verified).
- `flows/faultline-impact.yml` — optional minimal flow-registry v1 example
  (best-effort; see VERIFY list).
- `.gitlab-ci.yml` — catalog-sync include that publishes the above on a tag.

## Publish steps

Publishing is driven by the `catalog-sync` component on a **git tag push**.
Do these in order:

1. **Set the sync token (one-time).** In this project's CI/CD settings, add a
   variable:
   - `CATALOG_SYNC_TOKEN` = a **Project Access Token** with **`api`** scope and
     **Maintainer** role. (A **group-level** token is required to *enable* the
     agent inside member projects — see Enablement.)
   - Mark it **Masked** and **Protected**.

2. **Confirm the group_id** in both `.gitlab-ci.yml` (input `group_id`) and
   `agents/faultline-impact-reviewer.yml` (`consumers[].group_id`). See VERIFY.

3. **Tag and push to trigger the sync** (run from this `faultline/` repo root):

   ```sh
   git tag 0.0.1
   git push origin 0.0.1
   ```

   The tag pipeline runs `catalog-sync@0.0.20`, which reads `agents/` (and
   `flows/`) and registers/updates the entries in the AI Catalog for the target
   group.

4. **Bump the tag for later updates** (sync is tag-driven), e.g.:

   ```sh
   git tag 0.0.2
   git push origin 0.0.2
   ```

## Enablement (after publish)

After the agent appears in the AI Catalog, **enable it in the consumer
project(s)** so it responds to MR triggers:

- The `consumers` block in `agents/faultline-impact-reviewer.yml` declares the
  target `group_id`, the `projects` (currently `faultline-demo`,
  project_id `83369596`), and `trigger_types` (`mention`, `merge_request`).
- Enabling across member projects requires a **group-level access token**
  (`api` scope, Maintainer). Confirm the agent is listed for the group, then
  verify it is toggled on for `faultline-demo`.
- Smoke test: open/mention on an MR in `faultline-demo` and confirm a
  "Faultline — Change Impact" note is posted.

## TO VERIFY before publishing

- [ ] **Real top-level `group_id`.** Used in `.gitlab-ci.yml` and the agent's
  `consumers[].group_id`. Currently `134742282` for
  `anbuchelvanganesan.cse2024-group` — **unconfirmed**. Get it from the group's
  page / API (`GET /groups/:full_path`) and replace in BOTH files.
- [ ] **Tier permits AI Catalog publishing.** Confirm the active **Ultimate
  trial** tier allows publishing/consuming AI Catalog agents (trials can gate
  Duo/Catalog features). If gated, this layer is documentation-only until the
  tier is upgraded.
- [ ] **Declarative-only constraint.** Confirm understanding that ambient
  catalog agents **cannot run the Faultline binary** — this layer is an LLM-driven
  estimate using built-in tools, not the precise engine.
- [ ] **Token scopes/roles.** `CATALOG_SYNC_TOKEN` = Project Access Token,
  `api`, Maintainer; a **group-level** token for project enablement.
- [ ] **`faultline-demo` project_id** `83369596` and that it belongs to the
  same top-level group as `group_id`.
- [ ] **Flow form (`flows/faultline-impact.yml`).** The declarative agent schema
  is verified; the flow-registry v1 key set is best-effort — validate against
  `gitlab.com/components/ai-catalog` before relying on the flow.
