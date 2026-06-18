# Code signing policy

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org).

## What is signed

| Artifact | Status |
| :--- | :--- |
| `silt-v<version>-windows-installer.exe` | Authenticode-signed via SignPath on every [`Release`](.github/workflows/release.yml) workflow run. |
| `silt-v<version>-windows-portable.zip` | Unsigned. The inner `silt.exe` is the same binary as the one inside the installer; once installed it inherits the signed installer's identity. |

Linux artifacts are not signed — Linux desktops lack an equivalent SmartScreen gate, and distribution is via direct download.

## Verifying a signature

Signed binaries display **"SignPath Foundation"** as the publisher in the Windows UAC prompt and in *File → Properties → Digital Signatures*. Verify from PowerShell:

```powershell
Get-AuthenticodeSignature .\silt-v0.1.22-windows-installer.exe | Format-List
```

`Status` should be `Valid`; `SignerCertificate.Subject` should contain `CN=SignPath Foundation`.

New certificate identities accrue SmartScreen reputation per-file over time. Expect brief warnings on the first downloads of a new release; reputation is not inherited across versions.

## Team roles

> SignPath Foundation requires the project to publish the following three roles. **TODO:** replace the placeholders once your SignPath application is approved and you've configured the team.

- **Committers and reviewers** — members with push access to [`ChrisUFO/Silt`](https://github.com/ChrisUFO/Silt). *(Placeholder — list maintainers or link to a team.)*
- **Approvers** — the designated release approvers who click *Approve* in SignPath for each `release-signing` request. Currently: `@ChrisUFO`. *(Placeholder — add additional approvers as the team grows.)*

All team members use multi-factor authentication on both GitHub and SignPath.io.

## Privacy policy

Silt is a local-first application: it does not transfer any information to other networked systems unless explicitly requested by the user. No telemetry, automatic update checks, or crash reporting are bundled.

## Build provenance

Every signed release is built from a tagged commit on `main` by the [`Release` workflow](.github/workflows/release.yml). SignPath verifies — via origin verification on the `release-signing` policy — that the signed binary was produced by this workflow from the tagged source. A valid signature therefore implies the binary matches the source in this repository at the cited tag.

---

## Setup (one-time, manual)

> Tracked in [issue #140](https://github.com/ChrisUFO/Silt/issues/140). The sections below are the same checklist, kept here for future maintainers.

### Prerequisites

1. **LICENSE file at repo root.** SignPath Foundation requires an OSI-approved OSS license (MIT or Apache-2.0 fit this project). `README.md` already advertises MIT in a badge — make it real before applying.
2. **MFA on every maintainer's GitHub account.** SignPath Foundation requires this.
3. **Project already released and documented** — Silt v0.1.x releases and this README satisfy this.

### Apply

1. Submit the application at <https://signpath.org/apply>.
2. Wait for review (manual, days to weeks). SignPath may decline without justification per their terms.

### Configure SignPath.io (after approval)

1. Create a project:
   - **Slug:** `Silt` (must match `project-slug` in `release.yml`).
   - **Repository URL:** `https://github.com/ChrisUFO/Silt`
   - **Trusted Build System:** GitHub.com
2. Upload [`default.xml`](.signpath/artifact-configurations/default.xml) as the default artifact configuration.
3. Add two signing policies:
   - `test-signing` — for validating the integration on a throwaway build.
   - `release-signing` — origin verification enabled, restricted to `main` and `release/*`.
4. Designate yourself (or a team member) as Approver on `release-signing`.

### Wire to GitHub

In the repo settings (*Settings → Secrets and variables → Actions*):

- **Secret** `SIGNPATH_API_TOKEN` — an interactive API token from a SignPath user with the `Submitter` role on both signing policies.
- **Variable** `SIGNPATH_ORGANIZATION_ID` — your SignPath org ID (visible top-right in the SignPath UI).

The Release workflow auto-detects these and enables signing on the next `main` push. Without them, it builds unsigned (current behavior).

### Validate

1. Trigger `test-signing` against a branch build first to confirm the round-trip.
2. On the next merge to `main`, watch the Actions log: the `Sign installer with SignPath` step blocks until you approve in SignPath, then completes.
3. Confirm the published GitHub Release asset shows **"SignPath Foundation"** as publisher in Windows file properties.

### Rollback

Delete `SIGNPATH_API_TOKEN` and `SIGNPATH_ORGANIZATION_ID`. The Release workflow reverts to unsigned output with no code changes.
