default:
    @just --list

test *args:
    go test {{ args }} ./...
    npm --prefix runtimes test

lint:
    go vet ./...

fmt:
    nix fmt

check:
    nix flake check --log-format bar-with-logs

build:
    nix build .#herdr-mailer --log-format bar-with-logs

run *args:
    go run ./cmd/herdr-mailer {{ args }}
