# Request timeouts and safe error logging

## Status

Accepted

## Context

HTTP clients can stall while sending a request body. Database operations can wait
for a pool connection or a row lock. Returning a generic HTTP error without a
diagnostic record makes internal failures difficult to investigate. Database error
messages can contain credentials or the original URL.

## Decision

Use `http.Server.ReadTimeout` for reading headers and bodies, and keep the header
timeout at the smaller of five seconds and the read timeout. Use a child of the
request context with a deadline for each service call. For POST this budget begins
after JSON decoding. The existing service and repository propagate this context
to pool acquisition and SQL. Always cancel the child context when the handler exits.

Configure positive read, operation and write durations through environment
variables, defaulting to 5s, 3s and 10s. Require the write timeout to exceed the sum
of read and operation budgets. Set the HTTP idle timeout to 60s.

Return JSON `408` for a timed out body read where a response can still be sent,
and JSON `503` for an operation deadline. Preserve existing success and domain
error responses. Do not attempt an error response after client cancellation.

Log internal operation failures through the injected `slog.Logger`. Log a fixed
operation name and an allowlisted reason, including SQLSTATE when available.
Do not log raw errors, request paths, bodies, URLs or connection strings.
Expected validation failures and missing links do not produce internal-error logs.

## Consequences

Network timeouts and context deadlines cover different stages of a request.
Context cancellation is cooperative and is not a rollback guarantee. A write may
have committed before cancellation or a failed response; clients cannot safely
assume that retrying a create or redirect has no additional effect.

Safe categories provide less detail than raw database messages. SQLSTATE helps
diagnose database failures without exposing error details that can contain data.
