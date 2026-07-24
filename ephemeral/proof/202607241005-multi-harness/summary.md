# Multi-harness proof

Head under proof: the commit containing this file.

> This initial proof did not satisfy the requested definition of done. It did
> not preserve auditable command evidence, used a separate cache to bypass the
> installed watcher lock, did not replace ChatGPT, and did not exercise live
> rollback. See `remediation.md` for the corrected evidence and explicit
> remaining work. Do not use this file as completion evidence.

## Observable claims

- Production `check --json` discovered both official current releases and
  independently reported both installed bundles.
- ChatGPT activity was reported active for the executing Codex task while the
  open but dormant Claude application was reported idle.
- Automated coordinator proof showed a busy first harness did not prevent the
  ready second harness from being replaced.
- Automated installer proof prepared and atomically activated a dotted-version
  Claude bundle using Anthropic's pinned bundle and signing identity.
- Automated force proof reinstalled an equal version, refused downgrade, and
  attempted the second harness after a first-harness failure.
- Live force proof ran `update --force --harness claude --idle-window 1s`
  twice against installed version `1.24012.1`. Both runs downloaded and verified
  the official equal release, gracefully quit Claude, atomically replaced it,
  relaunched it, and passed readiness.
- Post-replacement inspection reported bundle
  `com.anthropic.claudefordesktop`, team `Q6L2SF6YDW`, a valid strict deep code
  signature, Gatekeeper `Notarized Developer ID` acceptance, build
  `1.24012.1`, and a running exact main executable.
- Live ChatGPT force proof reused a verified staged build `5828`, repeatedly
  reported the executing Codex task as active, and performed no quit or
  replacement before the proof command was canceled.

## Safety boundary

The executing Codex task made a complete live ChatGPT replacement unsuitable:
performing it would terminate the proof task itself. The real activity guard
was demonstrated, while ChatGPT activation and induced readiness rollback are
covered by the installer and coordinator tests.
