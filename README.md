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
```

Key flags:

| Flag | Default | Meaning |
|------|---------|---------|
| `--model` | `google/gemini-2.5-flash-lite` | OpenRouter model (also `$OPENROUTER_MODEL`) |
| `--debounce` | `5s` | settle time after a change before labelling |
| `--max-wait` | `30s` | relabel within this long even if a window keeps changing |
| `--workers` | `2` | max concurrent label requests |
| `--prompt` | – | file with a custom system prompt that replaces the built-in one |
| `--log-level` | `info` | `debug` \| `info` \| `warn` \| `error` (`--debug`/`--verbose` = debug) |

## Run as a systemd user service

The package installs `/usr/lib/systemd/user/niri-autolabel.service`.

```sh
# provide the API key
mkdir -p ~/.config/niri-autolabel
printf 'OPENROUTER_API_KEY=sk-or-...\n' > ~/.config/niri-autolabel/env

systemctl --user enable --now niri-autolabel
```

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
