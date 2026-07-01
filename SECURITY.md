# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities **privately**, not in public issues.

- Use GitHub's "Report a vulnerability" (Security Advisories) on this repository, or
- email **<dhira@wigataintech.com>**.

Include: affected version/commit, reproduction steps, and impact. We aim to
acknowledge within 3 business days and to provide a fix or mitigation timeline
after triage. Please give us reasonable time to release a fix before any public
disclosure.

## Supported versions

While `kay` is pre-1.0, only the latest tagged release and `main` receive
security fixes.

## Security model

`kay` is a local, single-operator CLI. Understanding its trust boundaries:

- **Local key storage.** Private keys are generated and stored on the operator's
  machine under the OS config dir (`~/Library/Application Support/kay` or
  `$XDG_CONFIG_HOME/kay`) as PEM files with `0600` permissions; `config.json`
  is `0600`. Keys are only as safe as the operator's account. Passphrase-
  protected keys are supported and prompted for without echo.
- **Authentication.** Public-key authentication only; password auth is out of
  scope. The matching public key must be installed in the server's
  `authorized_keys` (`kay install` prints the exact command).
- **Host-key verification.** Host keys are pinned in a `known_hosts` file. On
  first contact with an unknown host, `kay` shows the SHA-256 fingerprint and
  asks for explicit confirmation before trusting it (trust-on-first-use with
  consent). A later key mismatch is a hard error (possible MITM). The
  `--insecure` flag disables verification entirely and must only be used on
  throwaway/lab hosts.
- **Remote actions.** The dashboard can run destructive actions (kill a process,
  restart/stop a container). Each requires an explicit keypress **and** a `y`
  confirmation. Process targets are integer PIDs; Docker IDs/names are validated
  against `[A-Za-z0-9_.-]` and passed as discrete arguments — no shell
  interpolation of user-influenced data.
- **No telemetry.** `kay` makes no network connections other than the SSH
  connections you direct it to.

## Hardening recommendations

- Keep private keys passphrase-protected.
- Prefer ed25519 keys.
- Do not use `--insecure` against production hosts.
- Restrict the login user's privileges on the remote where possible.
