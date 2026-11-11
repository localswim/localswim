# localswim

A local swimlane board for organizing work. V2 serves the workspace and its boards
through one loopback service and port.

**[Roadmap and scope](ROADMAP.md)**

## Getting started

Use Go 1.27 to build the service and CLI.

```powershell
go build -o localswim.exe ./cmd/localswim
```

Set `LOCALSWIM_HUMAN_TOKEN` and `LOCALSWIM_BOT_TOKEN` in the server's environment.
Use distinct random base64url values of at least 32 bytes for each server start.
Start the service with a fresh data directory.

```powershell
.\localswim.exe serve --data C:\absolute\path\to\fresh-v2-data --port 0
```

Use `--port NUMBER` to select a port, or `--port 0` (the default) to choose an
available port automatically. The service prints its local URL. In your CLI
terminal, set `LOCALSWIM_URL` to that URL and `LOCALSWIM_TOKEN` to the chosen
Human or Bot token. `service status` reports the actual listening port.

```powershell
.\localswim.exe board create --name "Personal"
.\localswim.exe board list
.\localswim.exe workspace info
.\localswim.exe --help
```

Use `board create --json definition.json` for a custom workflow. Commands that read
JSON require a file path or `-` for standard input.
Export a board with `board export BOARD_ID FILE|-`. Use a new file path, or `-`
for standard output. The default format is JSON.

Use `board verify BOARD_ID` to verify the durable board through the service or
`board verify --data DIRECTORY BOARD_ID` to read the local snapshot offline.
Both forms write the full JSON report to standard output. A valid report exits
with status 0. An invalid report exits with status 1 and includes the issues.

[Development and conventions](CONTRIBUTING.md) · [API](docs/openapi.yaml) · [Milestones](milestone-plan.md)
