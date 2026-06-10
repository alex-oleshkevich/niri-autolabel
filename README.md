# niri-autolabel

A background daemon that keeps [niri](https://github.com/YaLTeR/niri) workspace
names in sync with their contents. When a workspace's windows change, niri-autolabel
asks an LLM (via [OpenRouter](https://openrouter.ai)) for a single word that
captures what the windows have in common, and sets it as the workspace name.
Empty workspaces have their label removed.

## How it works

- Watches `niri msg event-stream` and keeps an in-memory model of workspaces/windows.
- On a debounced change, sends the workspace's apps + window titles to OpenRouter
  and asks for one word naming the **common theme** of the windows.
- Sets the result with `niri msg action set-workspace-name` (or unsets it when empty).
- Labels are kept globally unique and only names niri-autolabel created are ever changed —
  names present at startup are left untouched and our labels are cleared on exit.

## Requirements

- `niri`
- An OpenRouter API key in `OPENROUTER_API_KEY`.

## Install

### From the AUR

```sh
paru -S niri-autolabel   # or yay -S niri-autolabel
```

### From source

```sh
go build -o niri-autolabel .
install -Dm755 niri-autolabel ~/.local/bin/niri-autolabel
```

## Usage

```sh
export OPENROUTER_API_KEY=sk-or-...
niri-autolabel                       # run
niri-autolabel --debug               # print the full prompt sent to the LLM + responses
niri-autolabel --dry-run             # print niri actions instead of applying them
niri-autolabel --max-cost-session 0.01  # stop requesting labels after 0.01 OpenRouter credits
```

Key flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `--model` | `google/gemini-2.5-flash-lite` | OpenRouter model (also `$OPENROUTER_MODEL`) |
| `--debounce` | `5s` | settle time after a change before labelling |
| `--max-wait` | `30s` | relabel within this long even if a window keeps changing |
| `--workers` | `2` | max concurrent label requests |
| `--max-cost-session` | `0` | max OpenRouter credits to spend per run; `0` disables the limit (also `$OPENROUTER_MAX_COST_SESSION`) |
| `--prompt` | – | file with a custom system prompt that replaces the built-in one |
| `--log-level` | `info` | `debug` \| `info` \| `warn` \| `error` (`--debug`/`--verbose` = debug) |

Cost reporting:

- Each successful OpenRouter request logs prompt tokens, completion tokens, total tokens, reported cost, and running session cost.
- On shutdown or after `--once`, niri-autolabel logs a session usage summary.
- Keep `--model` on a low-cost model and use `--max-cost-session` to stop new requests after tracked session cost reaches your limit.

## Run as a systemd user service

The package installs `/usr/lib/systemd/user/niri-autolabel.service`.

```sh
# provide the API key
mkdir -p ~/.config/niri-autolabel
printf 'OPENROUTER_API_KEY=sk-or-...\n' > ~/.config/niri-autolabel/env

systemctl --user enable --now niri-autolabel
```

If `OPENROUTER_API_KEY` is already set in your shell, import it into the systemd
user manager before starting the service:

```sh
systemctl --user import-environment OPENROUTER_API_KEY
systemctl --user restart niri-autolabel
```

For upgrades from early packages, the service also reads
`~/.config/autolabel/env`.

The service starts with `graphical-session.target`. For niri-autolabel to reach niri,
the session's `NIRI_SOCKET` (and `WAYLAND_DISPLAY`) must be visible to the user
manager. With uwsm-managed niri this happens automatically; otherwise import it
from your niri config, e.g.:

```
spawn-at-startup "systemctl" "--user" "import-environment" "WAYLAND_DISPLAY" "NIRI_SOCKET"
```

Stopping the service (SIGTERM) makes niri-autolabel remove the labels it set.

## License

MIT
