# Installer redesign worklog

correction: The primary installer is an agent; do not make the human reason about `GOBIN`, resolve a transient Go binary, and then accept a hidden self-copy.
decision: Publish a pinned `curl .../install.sh | bash` bootstrap that uses `go install` directly into the canonical Application Support directory and delegates only launchd registration to the installed binary.
decision: Treat installation as an application behavior: prove isolated install/refresh/uninstall before release, then prove the public tagged script against the real per-user LaunchAgent on this Mac.
review: Claude Fable design round 01 found contradictions around a test install-dir override, missing ChatGPT preflight, and unproved flag forwarding; remove the public path override, enforce preflight in script and CLI, inject only internal test dependencies, and prove configured plist output.
review: Claude Fable design round 02 found the shell test still lacked safe isolation; source the function body without its final invocation and override only home resolution in the test harness. Also parse forwarded app paths, publish a script checksum for agents, and document coupled release edits.
