# reflet

Local secret management that keeps values out of agent process memory.

Instead of injecting `sk-xxx...` into environment variables, `reflet` injects opaque references like `ref://openai-api-key`. A local proxy resolves them to real values only when an outbound request is about to leave your machine.

## Quick Start

```bash
git clone https://github.com/noooey/reflet.git
cd reflet
make build
```

Store a secret:

```bash
./bin/reflet set openai-api-key
# Enter secret: sk-xxxxxxxxxxxxxxxxxxxxxxxx
```

Run a command with references:

```bash
./bin/reflet run -e OPENAI_API_KEY=ref://openai-api-key -- \
  curl -H "Authorization: Bearer ref://openai-api-key" \
  https://api.openai.com/v1/models
```

The `curl` process only sees `OPENAI_API_KEY=ref://openai-api-key`. The real key is substituted by the local proxy at the last moment.

## Commands

| Command | Description |
|---------|-------------|
| `reflet set <name>` | Store a secret interactively |
| `reflet list` | List stored reference names |
| `reflet remove <name>` | Delete a stored secret |
| `reflet proxy` | Start the local proxy |
| `reflet run -e VAR=ref://name -- <cmd>` | Run a command with references |

## How It Works

```
┌─────────────┐     ref://...      ┌──────────────┐
│   Agent     │ ─────────────────> │ reflet Proxy │ ──> Real API
│  (no key)   │   HTTP_PROXY       │  (has key)   │
└─────────────┘                    └──────────────┘
```

- Secrets live in the OS keychain (macOS Keychain, Linux Secret Service, Windows Credential Manager)
- The agent process never holds plaintext values
- The proxy resolves `ref://` patterns in headers just before forwarding

## License

MIT (to be decided)
