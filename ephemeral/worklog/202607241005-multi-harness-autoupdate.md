# Multi-harness autoupdate worklog

decision: Full ChatGPT and Claude parity ships together under one coordinator; both harnesses default enabled and missing applications are skipped silently.
decision: Idle means no Codex, Claude Code, or Claude Cowork work is executing; open UI, chat, dormant sessions, and future schedules do not block.
decision: Forced reinstall bypasses only the newer-version check, never safety checks or downgrade protection.
correction: Keep planning discussion at behavioral contract and definition-of-done level; do not push release-source implementation choices back to the user.
friction: The planning artifact was initially only emitted in chat instead of written under ephemeral/projects -> material project plans should be persisted promptly when requested.
friction: The installed LaunchAgent held the normal cache lock during live proof -> use an isolated proof cache for an explicitly authorized one-shot replacement without disturbing the installed watcher.
decision: Live forced reinstall was completed twice for idle Claude; ChatGPT was not forced past its correctly active Codex-task guard.
decision: Live ChatGPT force proof reused its verified staged equal build and demonstrated repeated active-task deferral without quitting or replacing ChatGPT.
