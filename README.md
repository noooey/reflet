# reflet

`reflet` is a local secret management tool designed to keep secret values out of the memory space of the main agent or application process.

The core idea is simple:

- secrets are stored in the operating system's native secure storage
- applications receive only opaque references such as `ref://openai-api-key`
- a local proxy resolves those references only at the moment an outbound request needs the real value
- the main process handles references, not plaintext secrets

The name `reflet` comes from `reference` + `let`: let the process hold a reference, not the secret.

## Status

This repository contains the product specification and a working Go PoC. The CLI builds and runs on macOS, Linux, and Windows.

## Quick Start

### Build

```bash
git clone https://github.com/noooey/reflet.git
cd reflet
make build
```

### Store a secret

```bash
./bin/reflet set openai-api-key
# Enter secret: sk-xxxxxxxxxxxxxxxxxxxxxxxx
# stored openai-api-key as ref://openai-api-key
```

### List stored references

```bash
./bin/reflet list
# openai-api-key
```

### Run a command with references

```bash
./bin/reflet run -e OPENAI_API_KEY=ref://openai-api-key -- curl \
  -H "Authorization: Bearer ref://openai-api-key" \
  https://api.openai.com/v1/models
```

The `curl` process only sees `OPENAI_API_KEY=ref://openai-api-key`. The real API key is substituted by the local proxy just before the request leaves your machine.

### Start the proxy manually

```bash
./bin/reflet proxy
# reflet-proxy listening on http://127.0.0.1:17381
```

## Motivation

Most secret tools improve storage and sharing, but they still eventually expose the secret value to the target process.

Typical workflow today:

1. a secret manager stores the real value securely
2. a CLI fetches the value
3. the value is injected into an environment variable, config file, or process argument
4. the target process reads the plaintext value

That is already better than hardcoding secrets, but it still creates several exposure points:

- the target process can read the secret directly from its environment
- child processes may inherit the secret
- crash dumps may include the secret
- debug tooling may inspect the secret in memory
- logs or error messages may accidentally print the secret
- plugins, agent frameworks, and tool wrappers often gain full access once the secret is injected

This matters for local AI agents, automation tools, CLIs, and developer workflows where the operator may trust the main application logic but not every extension, subprocess, or debugging surface around it.

Tools such as 1Password, Bitwarden, and similar products solve secure storage and secret retrieval very well. However, once they hand a secret to a process, the process usually owns the plaintext. `reflet` is aimed at the narrower problem they do not fully solve: reducing plaintext secret exposure inside the main local process.

## Concept

`reflet` stores secrets in OS-native secure storage:

- macOS Keychain
- Linux Secret Service over D-Bus
- Windows Credential Manager

Instead of injecting secret values into environment variables, `reflet` injects reference strings:

- `OPENAI_API_KEY=ref://openai-api-key`
- `ANTHROPIC_API_KEY=ref://anthropic-prod`
- `DATABASE_PASSWORD=ref://db-password`

Applications are then run behind a local `reflet` proxy. The proxy intercepts outbound HTTP requests, detects configured secret references in headers, auth fields, query parameters, form fields, and optionally structured payloads, then substitutes the real values immediately before the request leaves the machine.

The intended property is:

- the application process sees only `ref://...`
- the proxy resolves the reference as late as possible
- the plaintext exists only inside the proxy process and only for the minimum time needed to construct the outbound request

## Design Goals

- Keep secret values out of the main process environment and general memory space.
- Reuse secure storage already provided by the operating system.
- Work well with existing CLI, agent, and developer tooling.
- Require minimal changes to applications that already talk over HTTP or HTTPS.
- Make secret references explicit and inspectable without revealing their values.
- Prefer local-only, single-user operation over centralized secret orchestration.

## Non-Goals

- Secret sharing across teams or organizations.
- Remote secret synchronization.
- Full replacement for enterprise secret vault platforms.
- Protection against a fully compromised local machine or a malicious root administrator.
- Transparent interception of every possible protocol on day one.

## High-Level Architecture

`reflet` has three main components:

1. CLI for storing and managing secret references.
2. Local secure storage adapter for the host OS keychain.
3. Local proxy for runtime reference resolution.

```text
+---------------------------+        +--------------------------------+
| User / Shell              |        | OS Native Secret Store         |
|                           |        |                                |
| reflet set api-key        |------->| macOS Keychain                 |
| reflet run ...            |        | Linux Secret Service / D-Bus   |
| reflet proxy              |        | Windows Credential Manager     |
+-------------+-------------+        +----------------+---------------+
              |                                       ^
              | launches process with ref:// values   |
              v                                       |
+-------------+---------------------------------------+---------------+
| Main App / Agent Process                                            |
|                                                                     |
| ENV: OPENAI_API_KEY=ref://api-key                                   |
|                                                                     |
| Never receives plaintext secret value                               |
+----------------------------+----------------------------------------+
                             |
                             | HTTP(S) via localhost proxy
                             v
+----------------------------+----------------------------------------+
| reflet Proxy                                                        |
|                                                                     |
| - accepts local connections over Unix Domain Socket / local pipe    |
| - inspects outbound requests                                        |
| - resolves ref://api-key from OS secret store                       |
| - substitutes plaintext only in the final outbound request          |
| - forwards request to remote server                                 |
+----------------------------+----------------------------------------+
                             |
                             v
+---------------------------------------------------------------------+
| Remote API / Service                                                 |
+---------------------------------------------------------------------+
```

## Runtime Model

The baseline runtime flow is:

1. User stores a secret with `reflet set`.
2. User launches an application with `reflet run`.
3. `reflet run` injects reference strings such as `ref://openai-api-key`.
4. `reflet run` also configures the child process to use the local `reflet` proxy.
5. The child process emits outbound HTTP requests through the proxy.
6. The proxy detects `ref://...` placeholders in request material.
7. The proxy retrieves the real secret from the OS keychain.
8. The proxy rewrites the request in-memory and sends it onward.

The main process should never need to call a "get secret value" API.

## Threat Model Summary

`reflet` is intended to reduce secret exposure to:

- the main application process
- subprocesses spawned from that application
- routine environment inspection
- accidental logging of environment variables
- accidental persistence of plaintext secrets in shell history or config files

`reflet` is not intended to protect against:

- malware with the same user privileges that can access the local proxy or keychain APIs directly
- kernel-level compromise
- root or administrator compromise
- debugging or instrumentation of the proxy process itself
- applications that use non-HTTP protocols unless explicitly supported

The tool is about shrinking the plaintext exposure surface, not eliminating all local compromise risk.

## Detailed Architecture

### 1. Secret Reference Namespace

References use a URI-like scheme:

```text
ref://<name>
```

Examples:

- `ref://openai-api-key`
- `ref://github-token`
- `ref://prod/stripe-secret`

Rules:

- names are case-sensitive by default
- allowed characters should be conservative and shell-safe
- `/` may be used for logical grouping
- references never embed the secret value

Potential future extension:

```text
ref://<namespace>/<name>?version=<n>
```

## 2. Secret Storage Layer

`reflet` stores a mapping from secret name to secret value in the platform store.

Required metadata:

- secret name
- creation timestamp
- update timestamp
- optional labels or notes

Important behavior:

- `reflet list` should return names and metadata only, never values
- `reflet set` should write directly to the native secret store
- no plaintext secret database should exist in the repository or local config by default

Implementation notes by platform:

- macOS: use Keychain Services
- Linux: use Secret Service API over D-Bus
- Windows: use Credential Manager

## 3. Process Launcher

`reflet run` is responsible for launching a child process with:

- chosen environment variables set to `ref://...`
- proxy environment configured
- socket path or connection metadata configured for the proxy

Example launcher contract:

```text
HTTP_PROXY=http://127.0.0.1:4319
HTTPS_PROXY=http://127.0.0.1:4319
REFLET_SOCKET=/tmp/reflet.sock
OPENAI_API_KEY=ref://openai-api-key
```

On Unix platforms, the preferred control channel for local components is a Unix Domain Socket. On Windows, the equivalent local-only transport may use a named pipe while preserving the same conceptual model.

## 4. Proxy

The proxy is the key differentiator.

Responsibilities:

- accept requests from local client processes
- parse HTTP requests
- identify placeholders matching the `ref://` scheme
- resolve those placeholders from the secret store
- substitute values only in supported locations
- forward the rewritten request upstream
- avoid persisting plaintext secrets to logs, metrics, or traces

Supported substitution targets in the initial design:

- HTTP headers such as `Authorization`, `X-API-Key`, `Api-Key`
- proxy authorization headers
- query string parameters
- `application/x-www-form-urlencoded` request bodies
- JSON string fields when explicitly enabled

HTTPS handling options:

1. Standard explicit HTTP proxy mode where the client sends plain HTTP requests or `CONNECT`.
2. Loopback TLS termination mode with local trust bootstrapping, if needed for richer rewriting.
3. SDK-specific transport adapters in future versions when generic proxying is insufficient.

The exact first implementation may start with plain HTTP proxying plus SDK and CLI workflows that are known to cooperate with explicit proxies.

## Why a Proxy Instead of Direct Substitution

If `reflet` simply replaced `ref://...` with plaintext before launching the process, the design would fail its main goal. The late-binding proxy is what preserves separation:

- launcher knows references
- proxy knows values
- application knows only references

That split is the core architectural constraint for the project.

## CLI Specification

The command line interface should be small and explicit.

## Global Form

```bash
reflet <command> [options]
```

Common options:

- `--help`: show command help
- `--version`: show version
- `--json`: machine-readable output where applicable
- `--quiet`: suppress non-error output

## `reflet set`

Store or update a secret value under a given name.

### Synopsis

```bash
reflet set <name>
reflet set <name> --from-stdin
reflet set <name> --value <plaintext>
```

### Behavior

- creates the secret if it does not exist
- updates the secret if it already exists
- prints the reference name, not the secret value
- should prefer interactive hidden input when no value source is provided

### Options

- `--from-stdin`: read the secret from standard input
- `--value <plaintext>`: set from a direct argument; supported but discouraged because shell history may capture it
- `--label <text>`: optional human-readable label
- `--note <text>`: optional note metadata

### Example

```bash
reflet set openai-api-key
reflet set github-token --from-stdin
printf '%s' "$TOKEN" | reflet set ci/token --from-stdin
```

## `reflet run`

Run a child process with secret references and proxy configuration.

### Synopsis

```bash
reflet run [options] -- <command> [args...]
```

### Behavior

- ensures the local proxy is available, starting it if necessary
- injects reference values into requested environment variables
- configures proxy environment for the child process
- executes the child process and returns its exit code

### Options

- `-e, --env <VAR=ref://name>`: inject a specific environment variable reference
- `--env-file <path>`: load environment variable mappings containing `VAR=ref://name`
- `--inherit-proxy`: reuse an already running proxy configuration
- `--no-proxy`: launch with references only; mainly for debugging
- `--socket <path>`: override Unix Domain Socket path for local control
- `--proxy-port <port>`: override explicit loopback TCP proxy port when used

### Examples

```bash
reflet run -e OPENAI_API_KEY=ref://openai-api-key -- codex
reflet run -e API_KEY=ref://demo-key -- node script.js
reflet run --env-file .env.reflet -- npm run dev
```

## `reflet proxy`

Run the local proxy as a foreground or background process.

### Synopsis

```bash
reflet proxy [options]
```

### Behavior

- starts the request-rewriting proxy
- binds only to local machine interfaces
- exposes local control and health information
- never prints plaintext secret values

### Options

- `--socket <path>`: Unix Domain Socket or platform-equivalent control endpoint
- `--listen <addr>`: explicit local listen address, default `127.0.0.1:<port>`
- `--foreground`: keep proxy attached to the terminal
- `--daemonize`: run in the background where supported
- `--allow-json-body`: enable JSON string field substitution
- `--log-level <level>`: `error`, `warn`, `info`, `debug`

### Example

```bash
reflet proxy --foreground --allow-json-body
```

## `reflet list`

List known secret references without revealing values.

### Synopsis

```bash
reflet list
reflet list --json
```

### Behavior

- returns stored secret names and metadata only
- does not show plaintext values
- may show labels, creation time, and last update time

### Example Output

```text
NAME              UPDATED
openai-api-key    2026-05-31T17:00:00Z
github-token      2026-05-30T09:12:00Z
```

## `reflet remove`

Delete a stored secret from the native secret store.

### Synopsis

```bash
reflet remove <name>
```

### Behavior

- removes the named secret
- should require confirmation in interactive mode unless `--yes` is provided

### Options

- `--yes`: skip confirmation

### Example

```bash
reflet remove github-token --yes
```

## Exit Codes

Suggested exit codes:

- `0`: success
- `1`: generic runtime or internal error
- `2`: invalid command usage
- `3`: secret not found
- `4`: native keychain backend unavailable or locked
- `5`: proxy startup or bind failure
- `6`: substitution failure for one or more required references

## Configuration

Minimal local configuration should live outside the repository, for example under:

- macOS/Linux: `~/.config/reflet/config.toml`
- Windows: `%AppData%\\reflet\\config.toml`

Suggested configuration fields:

- default proxy port
- default socket path
- logging level
- allowed substitution locations
- backend preferences

Config must never store plaintext secret values.

## Usage Examples

## Example 1: Running Codex with an OpenAI key reference

Store the key:

```bash
reflet set openai-api-key
```

Run Codex with only a reference in the environment:

```bash
reflet run -e OPENAI_API_KEY=ref://openai-api-key -- codex
```

Expected property:

- the Codex process sees `OPENAI_API_KEY=ref://openai-api-key`
- the real API key is substituted by the proxy only when Codex makes outbound API requests

## Example 2: Using curl through the proxy

```bash
reflet run -e OPENAI_API_KEY=ref://openai-api-key -- \
  curl https://api.openai.com/v1/models \
    -H 'Authorization: Bearer ref://openai-api-key'
```

In this case:

- `curl` never receives the plaintext key from `reflet`
- the proxy rewrites the `Authorization` header before forwarding the request

## Example 3: Running a Node.js script

```bash
reflet run -e API_KEY=ref://service-key -- node script.js
```

`script.js`:

```js
const headers = {
  Authorization: `Bearer ${process.env.API_KEY}`,
};

// process.env.API_KEY is "ref://service-key", not the real secret
```

The proxy later rewrites the outbound header.

## Example 4: Using an env file with references

`.env.reflet`:

```dotenv
OPENAI_API_KEY=ref://openai-api-key
ANTHROPIC_API_KEY=ref://anthropic-api-key
GITHUB_TOKEN=ref://github-token
```

Run:

```bash
reflet run --env-file .env.reflet -- node worker.js
```

## Example 5: Pre-starting the proxy

```bash
reflet proxy --daemonize
reflet run --inherit-proxy -e OPENAI_API_KEY=ref://openai-api-key -- codex
```

Useful when multiple tools should share one local proxy instance.

## Security Model

The security model must be explained clearly because the project makes a strong claim: the main application process should not see the plaintext secret.

## Security Boundary

The intended boundary is between:

- the main process
- the local proxy
- the OS-native secret store

Plaintext secrets are allowed only in:

- the native secret store implementation
- the proxy process memory during active substitution

Plaintext secrets should not appear in:

- the child process environment
- shell history from normal usage
- the repository
- config files
- standard logs
- process arguments in recommended usage

## Trust Assumptions

`reflet` assumes:

- the operating system account is reasonably trusted
- the native keychain backend provides meaningful local protection
- the proxy process is trusted more than the target application process
- local loopback and local socket exposure are controlled correctly

## What `reflet` Defends Against

- accidental plaintext exposure through environment variables
- accidental logging of copied secret values by the main application
- over-privileged plugins or toolchains that inspect env vars but do not control the proxy
- many forms of routine process introspection against the main process

## What `reflet` Does Not Defend Against

- a malicious process running as the same user that can talk to the proxy directly
- a malicious process running as the same user that can query the OS keychain directly
- a compromised proxy binary
- local privilege escalation or root compromise
- memory inspection of the proxy process itself
- application-layer misuse where a program sends a secret to the wrong remote destination after substitution

## Important Security Constraints

To preserve the design goal, implementations should follow these rules:

1. Never provide a general-purpose `reflet get <name>` command that prints plaintext values.
2. Never log substituted requests with resolved secrets.
3. Zero or overwrite temporary plaintext buffers where practical in the implementation language.
4. Bind proxy listeners to local interfaces only.
5. Restrict control channels so unrelated local users cannot drive substitution.
6. Prefer opt-in substitution for JSON body rewriting because generic payload rewriting can be risky and surprising.
7. Keep the substitution window as short as possible.

## Operational Considerations

- Proxy startup should fail closed when required references are missing.
- Users should be able to inspect which references were requested without seeing their values.
- Health endpoints and diagnostics must redact secrets aggressively.
- The implementation should expose metrics about substitutions and failures, not secret contents.

## Limitations

Several practical limitations should be stated upfront:

- some SDKs may ignore proxy settings
- end-to-end HTTPS rewriting may require careful local TLS handling
- non-HTTP clients may need dedicated adapters
- applications can still copy `ref://...` strings into logs, though those references are much safer than real values
- if an attacker already controls the local user account, this tool only adds limited friction

## Comparison to Existing Tools

`reflet` should complement, not attack, existing password managers and secret tools.

Comparison summary:

- 1Password / Bitwarden / similar tools: excellent at storage, sync, sharing, autofill, and retrieval
- `reflet`: focused on local process isolation from plaintext secret values during execution

Those tools answer:

- where are secrets stored?
- how are they shared?
- how does a user retrieve them?

`reflet` answers:

- how can a local process operate using a secret without directly holding its plaintext?

## Implementation Notes

A plausible implementation strategy:

- core daemon in Rust or Go for strong systems support
- thin platform-specific secret backend adapters
- explicit HTTP proxy with careful redaction
- small CLI front-end in the same binary

Why Rust or Go:

- strong support for systems programming
- good networking libraries
- straightforward static binaries
- reasonable control over memory lifetime and buffer handling

## Roadmap

## Phase 1: Core Local Workflow

- implement `set`, `list`, `remove`
- implement `proxy` for local HTTP traffic
- implement `run` with env and proxy injection
- support header and query parameter substitution
- support macOS and Linux first

## Phase 2: Better Developer Experience

- background proxy management
- JSON output for all commands
- structured audit events with redaction
- better `curl`, Node.js, and agent workflow docs
- Windows support

## Phase 3: Broader Protocol and SDK Coverage

- HTTPS interception modes with explicit local trust setup
- SDK-specific adapters for common AI and cloud clients
- policy rules for allowed destinations per reference
- body substitution controls by content type and path

## Phase 4: Hardening

- finer-grained local access controls
- buffer zeroization review
- security audits and threat-driven test suite
- replay and misuse protections for proxy control channels

## Future Ideas

- destination-bound secrets, where `ref://openai-api-key` can only be resolved for approved hosts
- time-scoped references for one-shot or short-lived use
- support for rotating secrets while long-running processes keep stable references
- integration with hardware-backed storage where available
- local developer UI for inspecting references, policies, and proxy status
- agent-specific transport shims beyond generic HTTP proxying
- secret usage policy engine with allow and deny rules
- structured redaction library reusable by other local tooling

## Open Questions

- What is the least invasive way to support HTTPS-heavy SDKs that do not cooperate well with explicit proxies?
- How should destination allowlists be expressed and enforced?
- Should `reflet run` always start a proxy automatically, or should that remain explicit?
- How much generic body rewriting is acceptable before behavior becomes surprising?
- What local authentication model should the proxy require from cooperating processes?

## Suggested Milestone Definition

An initial `v0.1` milestone should be considered successful if it can:

- store secrets in the host keychain
- launch a process with `ref://` environment values
- proxy a local HTTP request
- rewrite an `Authorization` header from a reference to a real value
- do all of the above without printing or returning the plaintext secret to the main process

## License

Open source license to be decided before the first public release.
