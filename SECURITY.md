# Security Policy

## Supported Versions

Ygo is in pre-1.0 development. Security fixes are applied to the latest tagged release on `main`. Older minor versions are not patched.

| Version | Supported |
| ------- | --------- |
| 0.9.x   | yes       |
| < 0.9   | no        |

## Reporting a Vulnerability

If you find a security issue, please report it privately rather than opening a public GitHub issue.

Email: ian00chechin@gmail.com

Please include:

- A description of the issue and its impact
- Steps to reproduce, or a minimal proof-of-concept
- The affected version (or commit hash)
- Any suggested mitigation, if known

I aim to acknowledge reports within 5 working days and provide an initial assessment within 14 days. Coordinated disclosure timelines are negotiated case by case; the default window is 90 days from initial report to public advisory.

## Scope

In scope:

- Wire-format parsing (V1 and V2 update decoders)
- WebSocket server: auth bypass, message handling, persistence layer
- Cross-language interop edge cases that could lead to memory exhaustion, panic, or corrupted document state
- `gomobile`-bound bindings (iOS, Android) where Go-side behavior creates a vulnerability

Out of scope:

- Vulnerabilities in dependencies. Please report those upstream (for example `modernc.org/sqlite`, `github.com/coder/websocket`). Dependency advisories are tracked via `govulncheck` in `.github/workflows/vuln.yml` (weekly schedule plus every push and PR).
- Issues that require a non-default, intentionally insecure configuration
- Theoretical denial of service from unbounded local input where the operator controls the producer

## Acknowledgements

Reporters will be credited in the release notes for the fix, unless anonymity is requested.
