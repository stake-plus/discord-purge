# Discord Message Purge Tool

A Go application that permanently deletes **all** traces of your activity on Discord:

- All messages in every server you belong to (text, voice, announcements, forums, stages)
- All thread messages (public, private, active, archived)
- All direct messages — **including hidden/closed DMs**
- All group DMs
- **All reactions you have placed on anyone's messages**

**Zero external dependencies** — uses only the Go standard library.

---

## ⚠️ DANGER — READ BEFORE USING

> **This tool permanently and irreversibly deletes data from your Discord account.**

- **Deleted messages are gone forever.** Discord provides no way to recover them.
- **Removed reactions cannot be restored.** There is no undo.
- **This covers your ENTIRE history** — every server, every DM, every reaction,
  going back to the day you created your account.
- **Automating user accounts may violate Discord's Terms of Service.** Use at
  your own risk. Your account could be flagged or banned.
- **This takes a long time.** If you have thousands of messages across many
  servers and DMs, expect the process to run for hours due to Discord API rate
  limits.

**Do not run this tool unless you are absolutely certain you want to erase
everything.**

---

## Prerequisites

You need **Go 1.21 or later** installed on your system.

### Installing Go

| Platform | Instructions |
|----------|-------------|
| **Windows** | Download the MSI installer from [https://go.dev/dl/](https://go.dev/dl/) and run it. It adds Go to your PATH automatically. |
| **Linux** | Download the tarball from [https://go.dev/dl/](https://go.dev/dl/) and follow the [official instructions](https://go.dev/doc/install), or install via your package manager (e.g. `sudo apt install golang-go` on Ubuntu/Debian, `sudo dnf install golang` on Fedora). |
| **macOS** | Download the pkg installer from [https://go.dev/dl/](https://go.dev/dl/), or use Homebrew: `brew install go`. |

Verify your installation:

```
go version
```

You should see something like `go version go1.21.0 ...` or newer.

---

## Project Structure

```
discord-purge/
├── src/
│   └── main.go              # All source code (single file, zero dependencies)
├── docs/
│   ├── GETTING_YOUR_TOKEN.md # Detailed token extraction guide
│   └── WHAT_GETS_DELETED.md  # Detailed breakdown of what is removed
├── bin/                      # Compiled binaries (created by build)
├── go.mod                    # Go module file
├── Makefile                  # Build automation (Linux/macOS)
└── README.md                 # This file
```

---

## Compilation

### Linux / macOS (with Make)

```bash
# Build for both Windows and Linux:
make all

# Build for Linux only:
make linux

# Build for Windows only:
make windows

# Build for your current platform:
make build

# Clean build artifacts:
make clean
```

Compiled binaries are placed in the `bin/` directory.

### Linux / macOS (manual)

If you don't have `make`, compile directly with `go build`:

```bash
# Build for Linux
go build -o discord-purge ./src

# Cross-compile for Windows from Linux
GOOS=windows GOARCH=amd64 go build -o discord-purge.exe ./src
```

### Windows (manual compilation)

Windows does not include `make` by default. The easiest approach is to compile
directly using `go build` from a terminal.

**PowerShell:**

```powershell
# Navigate to the project directory
cd C:\path\to\discord-purge

# Build for Windows
go build -o discord-purge.exe ./src
```

**Command Prompt (cmd.exe):**

```cmd
cd C:\path\to\discord-purge
go build -o discord-purge.exe ./src
```

**Cross-compile for Linux from Windows (PowerShell):**

```powershell
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o discord-purge ./src

# Reset back to Windows defaults
$env:GOOS = "windows"
$env:GOARCH = "amd64"
```

> **Optional:** If you want to use the Makefile on Windows, install
> [GNU Make for Windows](https://gnuwin32.sourceforge.net/packages/make.htm),
> or install it via [Chocolatey](https://chocolatey.org/):
> `choco install make`. Then you can use `make all`, `make windows`, etc.
> However, compiling manually as shown above is simpler and requires nothing
> extra.

---

## Getting Your Discord Token

The tool authenticates using your Discord **user token** (not a bot token, not
your password).

### Quick Method

1. Open [https://discord.com/app](https://discord.com/app) in your browser and log in
2. Press **F12** to open Developer Tools
3. Go to the **Network** tab
4. Type `api` in the filter box
5. Click on any channel or navigate around Discord to generate API requests
6. Click on any request to `discord.com/api/...`
7. In the **Headers** section, find `authorization`
8. Copy the token value (just the value, not the header name)

For a more detailed walkthrough with alternative methods, see
[docs/GETTING_YOUR_TOKEN.md](docs/GETTING_YOUR_TOKEN.md).

> **Security Warning:** Your token grants full access to your Discord account.
> **Never share it with anyone.** If compromised, change your Discord password
> immediately to invalidate all tokens.

---

## Usage

### Windows

```powershell
# Basic usage (will prompt for token):
.\discord-purge.exe

# With token as environment variable:
$env:DISCORD_TOKEN = "your_token_here"
.\discord-purge.exe

# With Discord data package for maximum DM coverage:
.\discord-purge.exe --data-package C:\path\to\discord-data-package
```

### Linux / macOS

```bash
# Basic usage (will prompt for token):
./discord-purge

# With token as environment variable:
export DISCORD_TOKEN="your_token_here"
./discord-purge

# With Discord data package for maximum DM coverage:
./discord-purge --data-package /path/to/discord-data-package
```

### Command-Line Options

| Option | Description |
|--------|-------------|
| `--data-package PATH` or `-d PATH` | Path to your extracted Discord data export for maximum DM coverage |
| *(no options)* | Runs interactively, prompts for token |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `DISCORD_TOKEN` | If set, the tool uses this token instead of prompting |

---

## What Gets Deleted

| Content | How |
|---------|-----|
| Your messages in all server text channels | Search API (automatic) |
| Your messages in threads (public, private, archived) | Search API (automatic) |
| Your messages in forum posts | Search API (automatic) |
| Your messages in voice/stage text chat | Search API (automatic) |
| Your messages in announcement channels | Search API (automatic) |
| Your messages in open/visible DMs | Search API (automatic) |
| Your messages in hidden/closed DMs | Discovered via relationships, then Search API |
| Your messages in historical DMs (deleted accounts, etc.) | Discovered via data package, then Search API |
| Your reactions on anyone's messages (servers) | Full channel scan |
| Your reactions on anyone's messages (DMs) | Full channel scan |

For a detailed breakdown of each phase and its limitations, see
[docs/WHAT_GETS_DELETED.md](docs/WHAT_GETS_DELETED.md).

### What Is NOT Deleted

- Messages in servers you have **already left** (rejoin first to delete them)
- Other people's messages (only your own)
- Your Discord account itself
- Server settings, roles, or channels

---

## How It Works

The tool runs in four phases:

### Phase 1 — Server Messages
Iterates every server you are a member of and uses Discord's search API to find
and delete every message you have authored. One search query covers all channel
types in the server (text, voice, threads, forums, etc.).

### Phase 2a — Open DMs
Fetches all DM and group DM channels currently visible in your DM list, then
searches and deletes your messages in each.

### Phase 2b — Hidden DMs
Discovers DMs you have closed/hidden by looking at your relationships (friends,
blocked users, pending friend requests). Force-opens each DM channel and deletes
your messages.

### Phase 2c — Data Package DMs (optional)
If you provide your Discord data export, parses `messages/index.json` to find
every DM channel that has ever existed on your account — including conversations
with deleted accounts and people you are no longer connected to.

### Phase 3 — Reaction Removal
Scans every message in every channel (servers and DMs) to find reactions you
placed, and removes them. This is the slowest phase because Discord has no API
to search by reactor.

---

## Rate Limiting

The tool includes built-in rate limit handling:

| Action | Delay |
|--------|-------|
| Between message deletions | 400ms |
| Between reaction removals | 300ms |
| Between search API calls | 2 seconds |
| Between pagination batches | 1 second |
| Rate limit retry | Automatic with `Retry-After` parsing, up to 5 retries |

---

## Disclaimer

This tool is provided as-is with no warranty. It is not affiliated with or
endorsed by Discord Inc. Automating user accounts may violate Discord's Terms of
Service. **Use entirely at your own risk.**
