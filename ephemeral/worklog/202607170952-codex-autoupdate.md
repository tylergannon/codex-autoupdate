# Codex autoupdate worklog

decision: Treat ChatGPT Desktop as the product name and com.openai.codex as the macOS bundle and local Codex runtime identity; verified from the installed app bundle.
decision: Observe Desktop task lifecycle from rollout task_started and task_complete records because the Desktop-managed app-server is a private stdio child with no attachable socket.
decision: Stage the full Sparkle enclosure while tasks run, verify the staged OpenAI signature and exact advertised build, then perform a second activity check immediately before graceful quit and atomic replacement.
friction: go mod download did not populate transitive checksums needed by go tool -> bootstrap repositories with go mod download all before invoking tool directives.
correction: run lefthook install explicitly inside the task worktree before its first commit, even though the common Git directory already has a hook.
