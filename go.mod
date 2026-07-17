module github.com/tylergannon/codex-autoupdate

go 1.26

tool (
	golang.org/x/tools/cmd/goimports
	golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize
)

require (
	github.com/spf13/cobra v1.10.2
	golang.org/x/sys v0.46.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/telemetry v0.0.0-20260625142307-59b4966ccb57 // indirect
	golang.org/x/tools v0.47.0 // indirect
)
