# A2A Agent Card example

A minimal, runnable server that exposes an [A2A Agent Card][a2a] at
`/.well-known/agent-card.json`.

## Run

```sh
go run ./examples/a2a-agent-card
```

The server listens on `127.0.0.1:8080` by default. Override with the `PORT`
environment variable:

```sh
PORT=9090 go run ./examples/a2a-agent-card
```

## Verify

```sh
curl http://127.0.0.1:8080/.well-known/agent-card.json
```

## Test

```sh
go test ./examples/a2a-agent-card/
```

[a2a]: https://a2aproject.github.io/A2A/latest/
