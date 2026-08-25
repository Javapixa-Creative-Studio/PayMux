# Security policy

PayMux handles payment credentials and moves money on behalf of the
applications that use it. Security reports are welcome and taken seriously.

## Reporting a vulnerability

**Please do not open a public issue for a security problem.**

Report it privately through GitHub's
[private vulnerability reporting](https://github.com/Javapixa-Creative-Studio/PayMux/security/advisories/new),
or by email to **security@javapixa.com**.

Please include:

- what the issue is and why it matters
- the steps or a proof of concept needed to reproduce it
- the PayMux version or commit, and your gateway environment
- any suggested fix, if you have one

What to expect:

| Stage                | Target                                    |
| -------------------- | ----------------------------------------- |
| Acknowledgement      | within 3 working days                     |
| Initial assessment   | within 7 working days                     |
| Fix or mitigation    | depends on severity; critical issues first |

We will keep you informed while we work, credit you in the advisory unless you
prefer otherwise, and let you know before we publish.

## Scope

In scope:

- authentication and session handling for administrators and API keys
- application isolation — any way one application can reach another's data
- gateway notification verification, and anything that lets a forged
  notification change a payment
- outbound webhook signing
- SSRF through webhook destinations
- encryption of gateway credentials and webhook secrets
- anything that lets a payout happen that should not: bypassing an
  application's disbursement permission or its spending limits, approving a
  payout as its own requester, or making one request move money twice
- injection, privilege escalation, or leakage of secrets through the API,
  logs or error messages

Out of scope:

- vulnerabilities in Midtrans or any other gateway — report those to the
  gateway's own security team
- issues that need an already-compromised host or database
- missing hardening that has no exploit path, absent a demonstrated impact
- denial of service through sheer volume against a deployment you do not run

## Deploying PayMux safely

PayMux is self-hosted, so several controls are yours to configure:

- **Set `PAYMUX_ENCRYPTION_KEY` to 32 random bytes and back it up.** Gateway
  server keys and webhook secrets are encrypted with it. If it is lost, those
  credentials must be re-entered; if it leaks, rotate it and re-enter them.
- **Serve PayMux over TLS.** Gateway notifications and API keys travel over it.
- **Leave `PAYMUX_ALLOW_PRIVATE_WEBHOOK_DESTINATIONS` set to `false`** unless
  your applications genuinely live on the same private network. It disables the
  SSRF protection on webhook destinations.
- **Remove the bootstrap administrator credentials** from the environment once
  the first account exists.
- **Restrict `PAYMUX_CORS_ORIGINS`** to the dashboard's own origin.
- **Keep the database private.** It holds encrypted credentials, payment
  history and audit records.
- Run the API and worker as non-root; the provided images already do.

## What PayMux never stores

- card numbers, CVVs, or any raw card credential
- gateway server keys in plaintext
- disbursement creator or approver keys in plaintext
- webhook signing secrets in plaintext
- authorization headers from gateway traffic

Card-sensitive operations are delegated to Midtrans Snap and Midtrans's
tokenization interfaces, which keeps PayMux — and your deployment — out of the
handling path for card data.
