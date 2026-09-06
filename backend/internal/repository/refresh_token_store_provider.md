# Refresh Session Authority Selection

`NewRefreshTokenStore` is the application Wire provider. It selects one store;
it does not copy sessions, promote a database, change Redis ACLs, or change live
configuration during startup. Migration 240 records the selected authority.

## Configuration

`JWT_REFRESH_TOKEN_STORE=redis` (or `jwt.refresh_token_store: redis`) is the
default for existing and new installations. Existing session JSON, signing
keys, token deadlines, and account data remain unchanged.

`JWT_REFRESH_TOKEN_STORE=postgres` requires an already committed, explicit
transition. Changing only the environment setting is rejected. Both modes
reject a missing/unreadable authority record, a standby PostgreSQL server, or a
read-only database session. PostgreSQL mode additionally checks the durable
schema and global revocation row. It never reads Redis as an authentication
fallback. Redis Sentinel automatic promotion is rejected while refresh sessions
remain Redis-authoritative.

The migration's one-way authority guard must remain installed. After activation,
nodes configured for Redis fail startup. Already-running upgraded Redis nodes
check the same record on every refresh-session operation. The SQL shared row
lock spans the Redis operation, so activation waits for operations already in
progress. A failed or ambiguous transaction acknowledgment returns neither
credential metadata nor successful revocation.

These checks apply to login/refresh-session storage, not each model inference
request or customer API-key lookup. Each guarded operation has a five-second
upper context deadline. They add a PostgreSQL round trip to Redis-mode session
operations; they are not advertised as a model latency optimization.

## Failure Handling

The PostgreSQL concrete store retains the optional issuance-preparer interface.
Its admission ticket must commit before generating a credential. The guarded
Redis store implements a separate issuance policy, without pretending legacy
sessions have PostgreSQL tickets. Both prohibit the handler's legacy JWT-only
fallback when storage or authority verification fails. User/family membership
verification must also succeed before a token pair is returned.

Direct `NewRefreshTokenCache` remains a low-level legacy constructor used by
tests; it is not the application provider and has no durable revocation
guarantee. A stale asynchronous Redis replica can still demonstrate the old
failure when this constructor is used without the application gate.

## Limits and Rollback

- The provider does not authenticate a legacy snapshot. Explicit adoption and
  fencing belong to the separately tested operator transition.
- A pre-upgrade binary does not understand the authority row or issuance policy.
  External fencing and upgrade coordination are still required before activation.
- Startup validation is not PostgreSQL HA. A lagging PostgreSQL promotion can
  lose committed revocations; synchronous replication, eligibility, old-primary
  fencing, and recovery must be accepted separately.
- Do not roll an activated installation back to a Redis-only binary or stale
  database snapshot. Preserve the selected authority and all durable tombstones.
- Existing stateless access JWTs retain their original semantics. This provider
  does not claim immediate global JWT invalidation.
- No production migration, environment update, or traffic switch is performed
  by these source changes or their tests.

## Verification

`refresh_token_store_provider_test.go` checks authority/configuration mismatch,
missing schema, standby/read-only rejection, retention of issuance interfaces,
the Sentinel gate, all eleven legacy operations, and ambiguous commits.
`refresh_token_store_provider_integration_test.go` uses disposable PostgreSQL and
Redis to exercise switching, already-running node rejection, no fallback,
durable issuance/revocation, and activation lock contention. Its explicit marker
fixture is not a legacy-adoption proof. Handler tests separately assert generic
503 responses without credentials when authority or membership checks fail.
