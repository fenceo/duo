# Jianzuo working agreement

- For each completed round of implementation/optimization, update the version and release notes, run the relevant tests, commit and push the source, and publish a new **stable GitHub Release** to `fenceo/jianzuo`. A local build or pushed commit alone does not complete a release. This is the maintainer's standing preference; an explicit instruction for the current task takes precedence.
- Use a new version/tag; never overwrite an existing published version. Publish the Windows installer, portable ZIP and both SHA-256 files. Verify uploaded asset names/sizes and confirm that the release is public, non-draft and latest. If blocked, report the blocker rather than claiming publication.
- Follow `docs/development.md` and `scripts/Publish-Release.ps1`. Keep generated web assets synchronized with committed TypeScript. Do not publish if tests fail or the build changes tracked source.
- Preserve user changes, runtime data, tasks, knowledge, credentials and environment configuration. Never include local data, logs, keys or deployment files in release assets.
- Use synthetic fixtures for routine regression. Real model calls need explicit authorization because they may transmit data and consume quota. Do not run the installer `-Full` test in the user's normal Windows account; use a disposable VM/user until its global side effects are isolated.
- Verify the existing service is idle before a requested local deployment. Keep a rollback executable and do not restart WSL or unrelated applications.
