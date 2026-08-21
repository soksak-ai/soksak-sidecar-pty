module github.com/soksak-ai/soksak-sidecar-pty

go 1.25.0

require (
	github.com/creack/pty v1.1.24
	github.com/soksak-ai/soksak-contract-control v0.0.1
	github.com/soksak-ai/soksak-contract-pty v0.0.1
)

replace github.com/soksak-ai/soksak-contract-control => ../../soksak-contracts/soksak-contract-control

replace github.com/soksak-ai/soksak-contract-pty => ../../soksak-contracts/soksak-contract-pty
