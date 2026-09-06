-- Safe no-op. This migration cannot defensibly reconstruct a weekly join
-- baseline from audit_logs: request_body is redacted, may be invalid or
-- truncated, and does not provide a guaranteed same-window cost/percentage
-- pair. An audit row can also predate the current account identity/window.
--
-- In particular, treating a missing snapshot_cost as zero, pairing an older
-- cost with the current percentage, scanning unbounded history, or replacing
-- an explicit estimator mode would silently corrupt quota state. Leave all
-- rows unchanged until a future migration has a trustworthy persisted source.
SELECT 1;
