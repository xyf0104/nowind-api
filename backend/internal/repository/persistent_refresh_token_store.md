# Persistent Refresh Token Store

Opt-in repository implementation only. `NewPersistentRefreshTokenStore(*sql.DB)`
implements the unchanged `service.RefreshTokenCache`; migration 238 creates its
tables. Its optional `service.RefreshTokenIssuancePreparer` interface is separate
from the unchanged Redis cache interface. There is no runtime Redis fallback or
startup backfill/cutover. See `persistent_refresh_token_transition.md` for the
separate explicit offline API, migration 240, destructive ACL fencing and strict
source limitations. Parent-owned production selection is a separate component.

## Required Issuance Sequence

1. Before generating family randomness, access tokens, or refresh tokens, call
   `PrepareRefreshTokenIssuance(ctx, userID)` on the persistent store.
2. Only after preparation commits, generate the credential and attach the returned
   ticket to `RefreshTokenData.Issuance`. Preserve that ticket on a Store retry.
3. Store atomically validates and spends this single-use ticket with the payload
   and indexes. Only committed success permits returning credentials to a client.
4. Rotation requires a new preparation before generating its replacement. An
   invalidated ticket must not be repaired by preparing again for already-created
   credentials or an already-revoked session. New independent login may prepare
   fresh admission normally.

The ticket is a PostgreSQL-generated UUID whose immutable DB row binds the user
and global/user generations. It has a five-minute DB-clock admission window,
which does not shorten the session's seven-day absolute deadline. Store never
prepares on demand and has no timestamp-only fallback. Ticket metadata is marked
`json:"-"` and Get/Consume omit it; serialized Redis/legacy payloads cannot supply
admission. The preparation API is a trusted internal API, not a public endpoint
or a legacy-import authorization mechanism.

## Authority and Transactions

- Use the writable PostgreSQL authority, never a lagging read replica.
- Store commits the hash payload, family row, ticket use, and SQL indexes together.
  The old `AddTo*TokenSet` hooks verify existing active membership only. Lists and
  `IsTokenInFamily` report active credentials, not historical Redis set entries.
- Consume locks and rechecks active state; only one transaction returns metadata.
  Failed or ambiguous COMMIT returns an error and no consumed metadata.
- Mutations set local `synchronous_commit = on` and acknowledge only after COMMIT.
  This does not configure PostgreSQL replication or guarantee survival of an
  asynchronous PostgreSQL promotion, rollback to backup, split brain, or lost WAL.
- Lock order is global, user, family, issuance, hash. Prepare and Store take the
  same global/user locks and validate the same monotonic generations. User locks
  serialize Store/Consume
  with DeleteUser, family locks serialize Store/Consume with DeleteFamily, and
  hash locks serialize Store/Consume with DeleteRefreshToken. The global shared
  lock allows independent users; global revocation takes an exclusive lock.
- A missing global revocation-state row prevents mutations from succeeding and
  makes active reads return no credentials. Database failures propagate as
  errors, not successful revocations or cache misses.

## Revocation and Lifetime

- Hash consumption/revocation and family revocation are permanent tombstones.
  Revoking an unknown hash/family creates a tombstone. Store is insert-only even
  for an active hash, so retries cannot alter metadata or extend a deadline.
- User/global revocation increments the corresponding BIGINT generation and
  revokes known affected families in the same transaction. Counter overflow is
  a database error, never a wrap/reset. Token reads also compare stored generations.
- A prepared-before-revocation ticket is permanently invalid, including for a
  never-stored family and even if CreatedAt is far in the future or rewritten.
  Forging current counters in an old ticket fails the persisted-ticket check.
  Preparation after revocation obtains fresh generations for an independent login.
- Family tombstones are permanent and apply even to tickets prepared after that
  family was revoked. Hash tombstones likewise survive fresh preparation. User
  IDs, family ownership, and ticket ownership cannot be reassigned by Store.
- CreatedAt and revocation audit timestamps are not authorization fences. Clock
  skew cannot make an old generation valid. Clocks still determine ordinary
  absolute expiry and ticket admission timeout, not revocation identity.
- Absolute ExpiresAt and FamilyExpiresAt are preserved at PostgreSQL microsecond
  precision (truncated, never extended). Effective validity is the earliest of
  those deadlines and CreatedAt + ttl. Family ownership and deadline are immutable.
- Metadata accepts only SHA-256 token/binding hashes, a generated 32-hex family
  ID, user ID, token version, timestamps, and internal issuance/generation metadata.
  No raw refresh credentials are persisted.
- No tombstone/generation/ticket cleanup exists. Rows are deliberately not
  cascaded from the users table. Retention growth requires a future compaction protocol;
  deleting expired rows without an issuance fence could permit resurrection.
- DeleteAllRefreshTokens is an explicit concrete repository API, not a change to
  the service interface. None of these methods invalidates stateless access JWTs.

## Rolling Upgrade Gate

Existing unadopted Redis-only credentials are rejected, including after Redis
promotion. Ordinary Store cannot adopt snapshots. The explicit bounded transition
API accepts only a dedicated, fenced standalone source and atomically activates
migration 240's authority marker. It does not support the existing replicated api
topology or prove the historical freshness of a newly restored standalone master.

The parent must own actual-environment source provenance, group-wide old-writer
fencing, readiness, a seven-day-compatible transition, production wiring, and
PostgreSQL HA acceptance before enabling this constructor. AuthService preparation
is a separate parent-owned change: it must precede all credential generation, including
rotation. The integration tests exercise that hook through the real constructor.
Switching existing reads still rejects unadopted Redis-only sessions. The parent
has an explicit production selector; activation remains gated on completed
adoption/readiness, not merely the presence of that wiring.

## Production Acceptance Gates

These gates are not satisfied merely by the repository tests:

1. Apply and verify migrations 238 and 240 on the authoritative writable PostgreSQL
   endpoint before enabling the production persistent selector. All auth instances must
   agree on that authority; a lagging read replica is not an alternative source.
2. Establish explicit legacy adoption and readiness that preserves existing
   sessions' original absolute deadlines, including the seven-day contract.
   Ordinary Store rejects Redis-only tokens and missing family deadlines. There
   is a restricted explicit transition API, not general HA adoption. A cache miss,
   database error, or replica snapshot must not trigger implicit adoption or a
   Redis authentication fallback.
3. Fence or drain old Redis-only auth writers before declaring the PostgreSQL
   revocation contract active. Mixed-version issuance, rotation, and revocation
   must not acknowledge operations outside the chosen authority. Any production
   wrapper must preserve the preparation interface and before-generation order.
4. Define rollback that preserves committed generations, ticket use, and hash/
   family tombstones. Flipping back to stale Redis or restoring an older PG
   snapshot is not a safe authentication rollback.
5. Separately accept PostgreSQL promotion eligibility, acknowledged-WAL survival,
   old-primary fencing, client routing, and old-node rejoin. The Redis recovery
   test neither promotes PostgreSQL nor exercises a watchdog VM. Local
   synchronous_commit alone does not prove zero-loss asynchronous PG failover.
6. Accept fail-closed login/refresh behavior during database outages, capacity
   and lock-wait monitoring, and retained-record growth. Do not add automatic
   tombstone/generation deletion. Stateless access-JWT invalidation remains a
   separate contract, not a capability of this refresh-token repository.

## Verification

Focused tests use disposable PostgreSQL/Redis testcontainers through the existing
repository harness. The recovery test reproduces a lost Redis DEL by pausing and
disconnecting its replica, acknowledging revocation, killing the primary, and
promoting the stale replica. The same PG authority and signing key then reject
the revoked token while an independent control rotates successfully. A Redis-only
credential is also rejected. The original deliberately red legacy test remains.
There is no test-side preparation adapter. Tests exercise direct AuthService
generation/rotation and preparation COMMIT failure, prepare/revoke races,
generation invalidation with future CreatedAt, forged/expired/cross-user tickets,
ticket single-use races, and rollback.
