# App-server control socket updater fix

correction: ChatGPT and Codex are one merged desktop application; verify the real process and bundle identity before prescribing lifecycle actions.
friction: the 2026-08-26 task lost command execution before diagnosis, and a later local fix stopped at an unpushed commit in the root checkout -> carry lifecycle fixes through review, release, installation, and live verification.
decision: treat PID 94932 as direct regression evidence because it still owns app-server-control.sock while executing the deleted build-7119 updater backup after the installed v0.2.2 watcher completed later updates.
decision: the fix must terminate control-socket holders only during an accepted application replacement, fail closed before activation, and apply through the shared installer to every configured harness where a Codex home is present.
