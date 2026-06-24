package main

import (
	"fmt"
	"log"
	"strconv"
	"time"
)

func validatePollConfig(cfg *Config) error {
	if cfg.Email == "" || cfg.Password == "" {
		return fmt.Errorf("poll: email and password are required in config.json")
	}
	if len(cfg.Watchlist) == 0 {
		return fmt.Errorf("poll: watchlist is empty — add at least one artist to config.json")
	}
	for i, wa := range cfg.Watchlist {
		if wa.ArtistID == "" {
			return fmt.Errorf("poll: watchlist[%d] has empty artistId", i)
		}
		if wa.Format != -1 && (wa.Format < 1 || wa.Format > 5) {
			return fmt.Errorf("poll: watchlist[%d] has invalid format %d (must be 1–5 or -1 for global)", i, wa.Format)
		}
	}
	return nil
}

func runPollMode() {
	cfg, err := readConfig()
	if err != nil {
		log.Fatalf("Failed to read config.: %v", err)
	}
	if err := validatePollConfig(cfg); err != nil {
		log.Fatalf("[poll] config error: %v", err)
	}
	if cfg.Format == 0 {
		cfg.Format = 4
	}
	if cfg.VideoFormat == 0 {
		cfg.VideoFormat = 5
	}
	if cfg.OutPath == "" {
		cfg.OutPath = "Nugs downloads"
	}
	if cfg.UseFfmpegEnvVar {
		cfg.FfmpegNameStr = "ffmpeg"
	} else {
		cfg.FfmpegNameStr = "./ffmpeg"
	}
	cfg.WantRes = resolveRes[cfg.VideoFormat]

	_, _, streamParams, err := initSession(cfg)
	if err != nil {
		log.Fatalf("[poll] Failed to initialize session: %v", err)
	}

	fmt.Println("Poll mode — authenticated. Starting watcher.")
	runPoller(cfg, streamParams)
}

func runPoller(cfg *Config, streamParams *StreamParams) {
	stateFile := cfg.StateFilePath
	if stateFile == "" {
		stateFile = defaultStateFile
	}
	interval := cfg.PollIntervalMins
	if interval <= 0 {
		interval = defaultPollIntervalMins
	}
	log.Printf("[poll] starting; %d artists, interval: %d min, state: %s",
		len(cfg.Watchlist), interval, stateFile)

	for {
		pollOnce(cfg, streamParams, stateFile)
		time.Sleep(time.Duration(interval) * time.Minute)
	}
}

func pollOnce(cfg *Config, streamParams *StreamParams, stateFile string) {
	state := loadState(stateFile)

	for _, wa := range cfg.Watchlist {
		log.Printf("[poll] checking artist %s (%s)", wa.ArtistID, wa.Name)

		containers, err := getArtistMeta(wa.ArtistID)
		if err != nil {
			log.Printf("[poll] ERROR fetching artist %s: %v", wa.ArtistID, err)
			continue
		}

		// First-run: artist not yet in state
		if _, seen := state[wa.ArtistID]; !seen {
			if !wa.BackfillAll {
				// Snapshot all current containers as known; download nothing (DEC-005)
				for _, meta := range containers {
					for _, container := range meta.Response.Containers {
						markKnown(state, wa.ArtistID, container.ContainerID)
					}
				}
				if err := saveState(stateFile, state); err != nil {
					log.Printf("[poll] WARN: failed to persist state for %s — will re-snapshot on next run: %v", wa.Name, err)
				}
				log.Printf("[poll] first run for %s: snapshotted %d containers (backfillAll=false)",
					wa.Name, len(state[wa.ArtistID]))
				continue
			}
			// backfillAll=true: initialize empty so download block runs for all containers
			state[wa.ArtistID] = []int{}
		}

		// Per-artist config override (shallow copy — never mutate global cfg)
		artistCfg := *cfg
		if wa.Format != -1 {
			artistCfg.Format = wa.Format
		}
		if wa.VideoFormat != -1 {
			artistCfg.VideoFormat = wa.VideoFormat
		}
		if wa.OutPath != "" {
			artistCfg.OutPath = wa.OutPath
		}

		knownSet := buildKnownSet(state[wa.ArtistID])
		for _, meta := range containers {
			for _, container := range meta.Response.Containers {
				if knownSet[container.ContainerID] {
					continue
				}
				albumIDStr := strconv.Itoa(container.ContainerID)
				log.Printf("[poll] NEW release: %s — %s (ID: %s)", wa.Name, container.ContainerInfo, albumIDStr)

				// album() re-fetches full metadata when albumID is non-empty (correct path for tracks)
				if err := album(albumIDStr, &artistCfg, streamParams, nil); err != nil {
					log.Printf("[poll] ERROR downloading %s: %v", albumIDStr, err)
					continue
				}

				// State written per successful download (crash safety — ARCHITECTURE invariant)
				markKnown(state, wa.ArtistID, container.ContainerID)
				knownSet[container.ContainerID] = true
				if err := saveState(stateFile, state); err != nil {
					log.Printf("[poll] ERROR saving state: %v", err)
				}

				msg := fmt.Sprintf("New release from %s: %s", wa.Name, container.ContainerInfo)
				if notifyErr := sendNotification(cfg.NotifyWebhookURL, cfg.NotifyWebhookType, msg); notifyErr != nil {
					log.Printf("[poll] ERROR sending notification: %v", notifyErr)
				}
			}
		}
	}
}
