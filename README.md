# AutoNugget

Automatically watches your Nugs.net artist pages, downloads new live recordings in your preferred quality, and sends a webhook notification — no manual checking required.

Built on top of [Syco54645/Nugs-Downloader](https://github.com/Syco54645/Nugs-Downloader). All original download functionality is preserved; AutoNugget adds a `poll` subcommand for unattended, scheduled monitoring.

---

## Downloads

| Platform | File | Notes |
|---|---|---|
| Windows | [`nugs-dl-windows-amd64.exe`](https://github.com/CtrlAltDelightOnGit/AutoNugget/releases/latest) | Also requires ffmpeg — `winget install ffmpeg` |
| Linux | [`nugs-dl-linux-amd64`](https://github.com/CtrlAltDelightOnGit/AutoNugget/releases/latest) | Also requires ffmpeg — `apt install ffmpeg` |
| Docker / Unraid | `docker pull ghcr.io/ctrlaltdelightongit/autonugget:latest` | ffmpeg included — no build step |

> Use the **[Setup Tools](https://ctrlaltdelightongit.github.io/AutoNugget/)** page to generate your `config.json` and get copy-paste commands for your platform.

---

## How It Works

1. You configure a watchlist of Nugs.net artist IDs in `config.json`
2. Run `nugs-dl.exe poll` (Windows) or `nugs-dl poll` (Linux/Docker)
3. On the first run, the current catalog is snapshotted — nothing is downloaded
4. On every subsequent poll cycle, any new releases are downloaded automatically and a notification is sent
5. The poll loop runs indefinitely; use Task Scheduler (Windows) or a Docker container (Unraid) to keep it running

---

## Setup

### 1. Create config.json

Place `config.json` in the same directory as the binary. A minimal poll mode config:

```json
{
  "email": "your@email.com",
  "password": "yourpassword",
  "format": 2,
  "videoFormat": 3,
  "outPath": "/mnt/user/media/Nugs",
  "useFfmpegEnvVar": true,
  "watchlist": [
    {"artistId": "196", "name": "Phish", "format": -1, "videoFormat": -1, "backfillAll": false},
    {"artistId": "297", "name": "Dead & Company", "format": 2, "videoFormat": -1, "backfillAll": false}
  ],
  "pollIntervalMins": 60,
  "notifyWebhookUrl": "https://discord.com/api/webhooks/YOUR_WEBHOOK",
  "notifyWebhookType": "discord",
  "stateFilePath": "/mnt/user/appdata/autonugget/state.json"
}
```

> **Windows / Linux users:** Use local paths instead — `"outPath": "downloads"`, `"stateFilePath": "auto_nugget_state.json"`, and `"useFfmpegEnvVar": false` (or omit it).

### 2. Config Field Reference

**Credentials and global settings:**

| Field | Type | Default | Description |
|---|---|---|---|
| `email` | string | — | Nugs.net account email. Required unless using `token`. |
| `password` | string | — | Nugs.net account password. Required unless using `token`. |
| `token` | string | `""` | Bearer token for Apple/Google accounts. See [token.md](token.md). |
| `format` | int | `4` | Global audio download quality. See format table below. |
| `videoFormat` | int | `5` | Global video download quality. See format table below. |
| `outPath` | string | `"Nugs downloads"` | Download directory. Created automatically if it doesn't exist. |
| `useFfmpegEnvVar` | bool | `false` | `true` = use `ffmpeg` from PATH. `false` = use `./ffmpeg` from binary directory. |
| `skipVideos` | bool | `false` | Skip video content. Useful for poll mode deployments that want audio-only downloads. |
| `forceVideo` | bool | `false` | Force video download when an artist page has both audio and video releases. |
| `videoOnly` | bool | `false` | Download video only on artist pages, skipping audio releases. |
| `skipChapters` | bool | `false` | Skip writing chapter markers for video downloads. |

**Poll mode settings:**

| Field | Type | Default | Description |
|---|---|---|---|
| `watchlist` | array | `[]` | Artists to monitor. See watchlist field reference below. |
| `pollIntervalMins` | int | `60` | Minutes between poll cycles. Recommended minimum: 60. |
| `artistCheckDelaySecs` | int | `0` | Seconds to wait between artist checks within a poll cycle. Set to `2`–`5` if you have a large watchlist or aggressive poll interval to avoid rate limiting. Omit or set to `0` to disable. |
| `notifyWebhookUrl` | string | `""` | Webhook URL for new-release notifications. Leave blank to disable. |
| `notifyWebhookType` | string | `"discord"` | Webhook format: `"discord"`, `"slack"`, or `"generic"`. |
| `stateFilePath` | string | `"auto_nugget_state.json"` | Path to the state file that tracks already-seen releases. |

**Watchlist entry fields:**

| Field | Type | Default | Description |
|---|---|---|---|
| `artistId` | string | — | Required. Numeric Nugs.net artist ID. See [Finding Artist IDs](#finding-artist-ids). |
| `name` | string | `""` | Human-readable label used in logs and notifications. |
| `format` | int | `-1` | Per-artist audio format override. `-1` = inherit global `format`. |
| `videoFormat` | int | `-1` | Per-artist video format override. `-1` = inherit global `videoFormat`. |
| `backfillAll` | bool | `false` | `false` = snapshot existing catalog on first run, download only future releases. `true` = download the artist's entire catalog on first run. |
| `outPath` | string | `""` | Per-artist download directory. `""` = inherit global `outPath`. |

**Optional environment variables:**

`NUGS_DEV_KEY` and `NUGS_CLIENT_ID` override the bundled API credentials. These are the same values published in the upstream open-source projects and are provided as a convenience — most users will never need to set them.

### 3. Format Values

**Audio (`format`):**

| Value | Quality |
|---|---|
| `1` | 16-bit / 44.1 kHz ALAC |
| `2` | 16-bit / 44.1 kHz FLAC |
| `3` | 24-bit / 48 kHz MQA |
| `4` | 360 Reality Audio / best available |
| `5` | 150 Kbps AAC |

> **Note on format 4 (360 Reality Audio):** This is a proprietary spatial audio format. Many players fall back to a lower-quality stream when 360RA is not natively supported. For lossless archival, prefer format 1 (ALAC) or 2 (FLAC).

**Video (`videoFormat`):**

| Value | Quality |
|---|---|
| `1` | 480p |
| `2` | 720p |
| `3` | 1080p |
| `4` | 1440p |
| `5` | 4K / best available |

---

## Poll Mode

### Poll Flags

```
nugs-dl poll [flags]
```

| Flag | Description |
|---|---|
| `--dry-run` | Log what would be downloaded without downloading anything. Useful for verifying your watchlist before first deployment. |
| `--config <path>` | Path to config.json (default: `config.json` in the current directory). |

### Finding Artist IDs

The artist ID is the number in the Nugs.net artist page URL.

Navigate to an artist's page on [play.nugs.net](https://play.nugs.net) — the URL will look like:

```
https://play.nugs.net/artist/196
```

The number at the end (`196`) is the `artistId` to use in your watchlist.

### Webhook Notifications

When a new release is detected and downloaded, AutoNugget POSTs a message to your configured webhook.

**Discord:** Create a webhook in your server's channel settings → Integrations → Webhooks. Set `notifyWebhookType` to `"discord"`.

**Slack:** Create an incoming webhook in your Slack workspace. Set `notifyWebhookType` to `"slack"`.

**Generic:** Any endpoint that accepts a POST with `Content-Type: application/json` and a `{"message": "..."}` body. Set `notifyWebhookType` to `"generic"`.

Leave `notifyWebhookUrl` blank to disable notifications entirely — downloads will still proceed.

### Running on Windows

```
.\nugs-dl.exe poll
```

To run unattended, set up a Task Scheduler task that runs `nugs-dl.exe poll` at startup or on a schedule. The process runs indefinitely and polls on its own interval — you don't need to schedule the poll cycles, just the initial launch.

**Updating:** Download the new binary from the [Releases page](https://github.com/CtrlAltDelightOnGit/AutoNugget/releases/latest), replace the existing `nugs-dl-windows-amd64.exe`, and restart the Task Scheduler task.

### Running on Linux

Make the binary executable and run:

```bash
chmod +x nugs-dl-linux-amd64
./nugs-dl-linux-amd64 poll
```

To run unattended via systemd, create `/etc/systemd/system/autonugget.service`:

```ini
[Unit]
Description=AutoNugget Nugs.net poller
After=network-online.target

[Service]
ExecStart=/opt/autonugget/nugs-dl-linux-amd64 poll
WorkingDirectory=/opt/autonugget
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now autonugget
```

Place your `config.json` in `/opt/autonugget/` alongside the binary, or pass `--config /path/to/config.json` to use a different location.

**Updating:** Download the new binary from the [Releases page](https://github.com/CtrlAltDelightOnGit/AutoNugget/releases/latest), replace the existing binary, and restart: `sudo systemctl restart autonugget`

### Running on Docker / Unraid

No build step required — pull the pre-built image directly:

**1. Create your appdata directory and config:**

```bash
mkdir -p /mnt/user/appdata/autonugget
# Create /mnt/user/appdata/autonugget/config.json
# Use the Setup Tools page or copy the example above — paths already match the broad mount.
```

**2. Pull and run:**

```bash
docker run -d \
  --name autonugget \
  --restart unless-stopped \
  --stop-timeout 600 \
  -v /mnt/user:/mnt/user \
  -v /mnt/user/appdata/autonugget/config.json:/app/config.json:ro \
  ghcr.io/ctrlaltdelightongit/autonugget:latest
```

> **`--stop-timeout 600`:** Video downloads can take 10–30 minutes. This gives AutoNugget time to finish a download gracefully on `docker stop`. Docker's default 10 s sends SIGKILL mid-download; the partial file is cleaned up on the next poll cycle but the download must repeat. Unraid users: set **Stop Timeout** to `600` in container settings instead.

The container defaults to poll mode. For one-off CLI downloads, install a wrapper script so you can just type `nugs-dl <url>`:

```bash
cat > /usr/local/bin/nugs-dl << 'EOF'
#!/bin/sh
docker run --rm \
  -v /mnt/user:/mnt/user \
  -v /mnt/user/appdata/autonugget/config.json:/app/config.json:ro \
  ghcr.io/ctrlaltdelightongit/autonugget:latest "$@"
EOF
chmod +x /usr/local/bin/nugs-dl
```

Then paste any download command directly:

```bash
nugs-dl https://play.nugs.net/release/23329
nugs-dl https://play.nugs.net/release/23329 -f 2 --audio-only
```

**Managing the container:**

```bash
docker logs -f autonugget                                      # follow live logs
docker restart autonugget                                      # restart after a config change
docker pull ghcr.io/ctrlaltdelightongit/autonugget:latest && docker restart autonugget  # update
```

### First-Run Behavior

On the **first** poll cycle for a new watchlist entry:

- **`backfillAll: false` (default):** All existing releases are recorded as already seen. Nothing is downloaded. Only releases that appear *after* this run will be downloaded.
- **`backfillAll: true`:** The artist's entire available catalog is downloaded immediately.

The state file (`stateFilePath`) records which releases have been seen. It is written after each successful individual download — if the process is interrupted mid-cycle, the next run retries any releases not yet recorded.

AutoNugget also writes history files (`<artistId>_Aud_history.txt` and `<artistId>_Vid_history.txt`) in the same directory as the state file. These track download URLs and guard against re-downloads even if the state file is reset. Do not delete the history files — they are the secondary guard against re-downloading content you already have.

---

## CLI Mode

All original download functionality works unchanged. Pass one or more Nugs.net URLs directly:

```
nugs-dl.exe https://play.nugs.net/release/23329
nugs-dl.exe https://play.nugs.net/release/23329 https://play.nugs.net/release/23790
```

Pass a text file of URLs:

```
nugs-dl.exe G:\urls.txt
```

CLI flags override config.json values:

```
nugs-dl.exe -f 2 -F 3 -o "D:\Music" https://play.nugs.net/release/23329
```

**CLI flags:**

| Flag | Description |
|---|---|
| `-f FORMAT` | Audio format (1–5) |
| `-F FORMAT` | Video format (1–5) |
| `-o PATH` | Output directory |
| `--force-video` | Force video download when audio and video co-exist |
| `--skip-videos` | Skip video content in artist URLs |
| `--audio-only` | Download audio only in artist URLs |
| `--video-only` | Download video only in artist URLs |
| `--skip-chapters` | Skip chapter markers for videos |

**Supported URL types:**

| Type | Example |
|---|---|
| Album | `https://play.nugs.net/release/23329` |
| Artist page | `https://play.nugs.net/artist/461` |
| Catalog playlist | `https://2nu.gs/3PmqXLW` |
| Exclusive Livestream | `https://play.nugs.net/watch/livestreams/exclusive/30119` |
| User playlist | `https://play.nugs.net/library/playlist/1261211` |
| Video | `https://play.nugs.net/#/videos/artist/1045/Dead%20and%20Company/container/27323` (wrap in quotes on Windows) |
| Webcast | `https://play.nugs.net/#/my-webcasts/5826189-30369-0-624602` |

---

## FFmpeg Setup

FFmpeg is required for TS → MP4 conversion (videos) and HLS-only tracks.

**Windows:** Download a GPL build from [BtbN/FFmpeg-Builds](https://github.com/BtbN/FFmpeg-Builds/releases). Place `ffmpeg.exe` in the same directory as `nugs-dl.exe`, or set `useFfmpegEnvVar: true` in config.json to use an FFmpeg already on your PATH.

**Linux:** `sudo apt install ffmpeg`, then set `"useFfmpegEnvVar": true` in your config.json (ffmpeg will be on PATH, not in the binary directory).

---

## Apple / Google Account Auth

If your Nugs.net account is linked to Apple or Google, use a `token` instead of `email`/`password`. See [token.md](token.md) for instructions on extracting your token.

For **unattended poll mode**, also set `email` and `password` alongside `token`. Nugs.net tokens expire after approximately 10 hours. When AutoNugget detects a 401 response, it automatically re-authenticates using your email and password. Without credentials, a token-only daemon will stop downloading at token expiry and require a manual restart.

---

## Disclaimer

- I will not be responsible for how you use AutoNugget.
- Nugs and Nugs.net are registered trademarks of their respective owners.
- AutoNugget has no partnership, sponsorship, or endorsement from Nugs.
