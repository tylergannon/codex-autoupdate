# Update Go tools to latest

correction: Tool dependencies must be resolved with `@latest`; do not invent a deliberate-version policy from the concrete version that Go records in `go.mod`.
friction: `golang.org/x/tools` was still at v0.47.0 even though v0.48.0 predated the repository -> use explicit `go get -tool <tool>@latest` when adding or refreshing tool dependencies.
decision: Go tool directives remain reproducibly recorded at the concrete version resolved by `@latest`; the required resolution command is `go get -tool <tool>@latest`, not a bare `go get -tool <tool>`.
