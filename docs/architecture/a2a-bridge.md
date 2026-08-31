# A2A bridge architecture

Status: experimental

October Bus is a coordination runtime and bridge. A2A is the protocol used at the boundary between compatible agents. MCP remains the primary adapter surface for coding harnesses.

## Compatibility target

The first bridge targets A2A 1.0 over HTTP+JSON. JSON-RPC and gRPC are not advertised until they are implemented and tested.

| Component | Pin |
| --- | --- |
| A2A specification | `v1.0.0` at `173695755607e884aa9acf8ce4feed90e32727a1` |
| A2A Go SDK | `v2.4.0` at `5736cc7c76905476840257b2c3b0f84a6fea8134` |
| A2A TCK | commit `107a5fd4ccc129b9d9335c797379779834968cd9` |

SDK versions and A2A protocol versions are independent. The bridge records both.

## Boundaries

- The October Bus domain model remains canonical inside the runtime.
- A2A types are translated at transport boundaries.
- An Agent Card describes an agent. It does not grant access to the agent or its scope.
- Public cards contain no execution IDs, credentials, prompts, local paths, or private context.
- A deployment chooses which agent cards to publish. The shared daemon does not enumerate agents publicly.
- A deployment gives each published agent a stable interface URL. Execution IDs are never public identity.
- Agent interfaces use bearer authentication. Loopback HTTP is allowed for local development. Remote interfaces require HTTPS.
- October Bus shared work items are a coordination pool. They are not A2A Tasks. A2A Tasks represent one delegated interaction and its result stream.

The initial package generates and serves read-only Agent Cards. It does not implement A2A message or task operations yet.

### Handler caching and conditional requests

The handler returned by `a2abridge.NewAgentCardHandler` defaults to a
60-second `Cache-Control: public, max-age=60` policy with the current
time as `Last-Modified`. Use `a2abridge.NewAgentCardHandlerWithOptions`
to override either value via `HandlerOptions.CacheLifetime` and
`HandlerOptions.LastModified`. The cache lifetime must be non-negative
and at most 24 hours; the constructor rejects other values.

The handler honours both `If-None-Match` and `If-Modified-Since`. It supports
weak tags, tag lists, and wildcard cache validation. `If-None-Match` takes
precedence when both headers are present. The `ETag`, `Cache-Control`, and
`Last-Modified` headers are computed once and remain consistent for every
request served by a handler instance.

## Extensions

Core A2A behavior is implemented before any October-specific extension. Extension identifiers are not published until their domain and lifecycle are established. Extension support will have separate conformance evidence from core A2A support.

## Version policy

October Bus is pre-1.0. Bridge APIs may change as the A2A server surface is implemented. Public compatibility claims require the applicable A2A TCK checks and released interoperability evidence.
