# ConPTY backend contract probe

This independent module applies one native Windows launch, input/output, resize and natural-exit
contract to candidate ConPTY backends. It is diagnostic test code and is not a production dependency.

Run it on Windows with `go test -count=1 -v ./...`.
