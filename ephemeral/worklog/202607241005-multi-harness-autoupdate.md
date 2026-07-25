# Multi-harness autoupdate worklog

decision: Full ChatGPT and Claude parity ships together under one coordinator; both harnesses default enabled and missing applications are skipped silently.
decision: Idle means no Codex, Claude Code, or Claude Cowork work is executing; open UI, chat, dormant sessions, and future schedules do not block.
decision: Forced reinstall bypasses only the newer-version check, never safety checks or downgrade protection.
correction: Keep planning discussion at behavioral contract and definition-of-done level; do not push release-source implementation choices back to the user.
friction: The planning artifact was initially only emitted in chat instead of written under ephemeral/projects -> material project plans should be persisted promptly when requested.
friction: The installed LaunchAgent held the normal cache lock during live proof -> use an isolated proof cache for an explicitly authorized one-shot replacement without disturbing the installed watcher.
decision: Live forced reinstall was completed twice for idle Claude; ChatGPT was not forced past its correctly active Codex-task guard.
decision: Live ChatGPT force proof reused its verified staged equal build and demonstrated repeated active-task deferral without quitting or replacing ChatGPT.
correction: Do not declare implementation complete or request review while required live proof remains incomplete; unmet proof is unfinished work, not a safety-boundary footnote.
review: The installed watcher owns the default lock for its lifetime, so the documented one-shot command originally failed and the isolated-cache proof bypassed the single-coordinator guarantee.
decision: One-shot commands now request a cooperative takeover with a cache marker and `SIGUSR1`; the daemon yields only between safe coordinator steps, and launchd remains loaded to resume after the marker is removed.
review: Live process inspection found Claude Code-launched task PIDs 17911 and 17914 still using deleted task-output paths while the original detector reported Claude idle.
decision: Claude activity combines Desktop session identifiers, standalone Claude Code processes, and live standard streams under the per-user Claude task root; dormant metadata alone remains idle.
review: Rollback discarded failed-replacement shutdown errors and could restore files while the new process survived.
decision: Activation and rollback now share the same graceful-quit, exact-PID `SIGTERM`, and confirmed-process-absence path; rollback does not move bundles if shutdown fails.
correction: When the user explicitly authorizes destructive live proof, exercise and repair the real installation directly; do not invent disposable-copy scaffolding to avoid a failure the rollback contract is supposed to handle.
friction: Detached proof jobs from an interrupted turn later contended for the coordinator lock -> inventory and reconcile exact background proof processes before launching another one-shot command.
review: A configured Claude app-path override caused the canonical Claude Desktop executable and helpers to be parsed as standalone Claude Code because their paths contain spaces.
decision: Claude CLI detection now excludes processes launched from any `Claude.app/Contents` bundle before tokenizing command text.
decision: Live equal-version force replacement and controlled rollback completed against both real applications; pre/post inodes, PIDs, quarantine, strict signatures, signing teams, Gatekeeper acceptance, and residue cleanup were recorded.
review: A second one-shot could send default-fatal `SIGUSR1` to a first one-shot because the lock recorded only its PID.
review: Round three found that setup/recovery errors still aborted watcher construction before healthy harnesses could run, and that repeated successful forced reinstallation was not preserved. Setup failures are now represented as per-harness watcher errors so the coordinator retains failure isolation; the equal-version watcher test now performs two consecutive passes; a second real Claude force pass is preserved and the second ChatGPT pass is scheduled for the first safe idle window.
decision: The second real ChatGPT force pass staged while the proof task was active, deferred correctly, then completed after the task yielded; its changed inode/PID, zero exit, strict signature, Gatekeeper acceptance, and restored normal daemon state are preserved.
decision: Round-four independent adversarial review found no remaining findings after rechecking the entire implementation, proof, installed runtime, and fresh automated gates.
decision: Lock metadata now distinguishes daemon and one-shot owners; only a cooperative daemon may be signaled, while concurrent one-shots fail without a takeover marker or signal.
review: Process-only Claude activity vanished without a completion timestamp, allowing immediate activation on the first inactive poll.
decision: Each watcher now remembers observed activity and starts its own fresh idle clock on the first subsequent inactive poll.
review: Process interruption between the backup and activation renames could leave the canonical app missing and then silently skipped.
decision: Watcher startup now verifies, restores, and relaunches exactly one retained backup for a missing canonical app and fails closed on ambiguous backups.
