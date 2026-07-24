correction: The failure is not two concurrently running app versions. A scheduled-task confirmation dialog prevents ChatGPT Desktop from exiting, so activation never begins.
friction: macOS denied terminal access to the Desktop screenshot and the Computer Use bridge failed to start -> base the fix on the confirmed quit-veto behavior and source/runtime evidence; do not assume screenshot details or automate localized dialog text.
decision: Avoid UI scripting for the scheduled-task quit dialog because a user LaunchAgent cannot reliably depend on Accessibility permission, button labels, or dialog structure.
decision: Preserve the normal Apple-event quit path, but when it is refused, send SIGTERM only to the re-resolved ChatGPT main PID; never send SIGKILL and never replace the bundle until process absence is confirmed.
evidence: The installed watcher repeatedly logged AppleScript error -128 User canceled immediately after the scheduled-task quit request, confirming that the application vetoed normal termination.
correction: golangci-lint upstream explicitly recommends the installed binary and warns against go tool; the repository hook is already correct with golangci-lint run ./....
proof_blocker: A branch-built check reports installed build 5828 equals available build 5828, so the real update/restart path cannot be exercised until OpenAI publishes a newer stable build.
