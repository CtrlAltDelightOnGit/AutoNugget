# AutoNugget

Automatically watches your Nugs.net artist pages, downloads new live recordings in your preferred quality, and sends a webhook notification — no manual checking required.

Built on top of [Syco54645/Nugs-Downloader](https://github.com/Syco54645/Nugs-Downloader). All original download functionality is preserved; AutoNugget adds a `poll` subcommand for unattended, scheduled monitoring.

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
  "outPath": "downloads",
  "watchlist": [
    {
      "artistId": "196",
      "name": "Phish",
      "format": -1,
      "videoFormat": -1,
      "backfillAll": false,
      "outPath": ""
    }
  ],
  "pollIntervalMins": 60,
  "notifyWebhookUrl": "https://discord.com/api/webhooks/YOUR_WEBHOOK",
  "notifyWebhookType": "discord",
  "stateFilePath": "auto_nugget_state.json"
}
```

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

**Poll mode settings:**

| Field | Type | Default | Description |
|---|---|---|---|
| `watchlist` | array | `[]` | Artists to monitor. See watchlist field reference below. |
| `pollIntervalMins` | int | `60` | Minutes between poll cycles. Recommended minimum: 60. |
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

### 3. Format Values

**Audio (`format`):**

| Value | Quality |
|---|---|
| `1` | 16-bit / 44.1 kHz ALAC |
| `2` | 16-bit / 44.1 kHz FLAC |
| `3` | 24-bit / 48 kHz MQA |
| `4` | 360 Reality Audio / best available |
| `5` | 150 Kbps AAC |

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

### Running on Docker / Unraid

Build for Linux:

```
GOOS=linux GOARCH=amd64 go build -o nugs-dl .
```

Mount your `config.json`, state file, and download directory as volumes. Example `docker run`:

```
docker run -d \
  -v /path/to/config.json:/app/config.json \
  -v /path/to/state.json:/app/auto_nugget_state.json \
  -v /mnt/media/nugs:/app/downloads \
  autonugget
```

A Dockerfile is planned for a future release.

### First-Run Behavior

On the **first** poll cycle for a new watchlist entry:

- **`backfillAll: false` (default):** All existing releases are recorded as already seen. Nothing is downloaded. Only releases that appear *after* this run will be downloaded.
- **`backfillAll: true`:** The artist's entire available catalog is downloaded immediately.

The state file (`stateFilePath`) records which releases have been seen. It is written after each successful individual download — if the process is interrupted mid-cycle, the next run retries any releases not yet recorded.

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

**Linux:** `sudo apt install ffmpeg`

---

## Apple / Google Account Auth

If your Nugs.net account is linked to Apple or Google, use a `token` instead of `email`/`password`. See [token.md](token.md) for instructions on extracting your token.

---

## Disclaimer

- I will not be responsible for how you use AutoNugget.
- Nugs and Nugs.net are registered trademarks of their respective owners.
- AutoNugget has no partnership, sponsorship, or endorsement from Nugs.
