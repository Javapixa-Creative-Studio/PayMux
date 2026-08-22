# Contributing to PayMux

Thanks for considering a contribution. PayMux is payment infrastructure, so the
bar for correctness is high — but the workflow is ordinary.

## Getting set up

Requirements: Go 1.25+, Node 20+, Docker.

```bash
git clone https://github.com/Javapixa-Creative-Studio/PayMux.git
cd PayMux
cp .env.example .env
echo "PAYMUX_ENCRYPTION_KEY=$(openssl rand -hex 32)" >> .env

make db                  # start PostgreSQL
go run ./apps/api        # applies migrations on startup
go run ./apps/worker
make dashboard-install && make dashboard-dev
```

## Before you open a pull request

```bash
make lint              # go vet + gofmt
make lint-full         # golangci-lint, as CI runs it
make test              # unit tests
make dashboard-lint    # eslint + typecheck
make dashboard-test
```

Integration tests need a disposable database:

```bash
docker run -d --name paymux-test-pg -e POSTGRES_USER=paymux \
  -e POSTGRES_PASSWORD=paymux -e POSTGRES_DB=paymux -p 55432:5432 postgres:17-alpine

PAYMUX_TEST_DATABASE_URL="postgres://paymux:paymux@localhost:55432/paymux?sslmode=disable" \
  make test-integration
```

CI runs all of the above.

`make lint-full` needs golangci-lint:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

The configuration in `.golangci.yml` enables the linters that catch real
defects and leaves the stylistic ones off. Where a finding is deliberate, the
exception carries a `//nolint` with the reason at the site rather than a
blanket waiver in the config — if you need one, say why in the comment.

## What we look for

**Tests that could fail.** A test that only exercises the happy path tells us
little. The properties worth testing here are the ones money depends on:
idempotency, isolation between applications, state transitions that must not go
backwards, signature verification, retry behaviour.

**Comments that explain why.** The code says what it does. A comment should say
why it does it that way — the constraint, the failure it prevents, the
alternative that was rejected. Comments that restate the next line are noise.

**Errors that stay opaque to clients.** Database errors, gateway internals and
stack traces never reach an API response. Add a domain error and map it in
`internal/api/errors.go`.

**Ownership resolved from authentication.** Never from a request body. An
application id supplied by a caller is not evidence of anything.

## Areas with specific rules

### Database changes

Migrations are forward-only and never edited once applied — the migrator
verifies a checksum and refuses to run if an applied migration has changed. Add
a new file in `migrations/` named `NNNN_description.sql`.

Prefer a constraint over a check in Go where correctness is at stake. PayMux
relies on unique indexes for idempotency precisely because only the database
can arbitrate between concurrent writers.

### Gateway adapters

Everything gateway-specific belongs inside the adapter package. The rest of
PayMux must not learn a gateway's request shapes, status names or endpoints.

If a gateway supports something others do not, add a capability interface in
`internal/gateway` rather than a method every adapter must stub out.

Never send unvalidated caller input to a gateway. Gateway options are typed and
rejected on unknown fields.

### Money

Amounts are integers in the currency's minor unit. Conversion to and from a
gateway's decimal strings happens only in `internal/money`, which uses no
floating point. If you find yourself writing `float64` near an amount, stop.

### Secrets

Wrap sensitive strings in `crypto.Secret` so they cannot leak through logging
or JSON. Anything stored must be sealed with the `crypto.Sealer` or hashed.
Never add a secret to a response type in `internal/api` — apart from the two
deliberate cases, an API key and a webhook secret at the moment of creation.

## Commit and pull request style

- One logical change per pull request.
- Explain the problem in the description, not only the diff.
- Reference the PRD section when implementing a specified behaviour.
- Keep the public API stable: `/api/v1` is a contract, and event type names are
  matched on by downstream applications. Add; do not repurpose.

## Reporting bugs

Open an issue with what you expected, what happened, and enough detail to
reproduce it. Include the PayMux version, your gateway environment (sandbox or
production) and the `request_id` from the error response if you have one.

For security issues, follow [SECURITY.md](SECURITY.md) instead — please do not
open a public issue.

## Code of conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
