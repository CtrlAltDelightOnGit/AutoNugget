package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
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
		if wa.VideoFormat != -1 && (wa.VideoFormat < 1 || wa.VideoFormat > 5) {
			return fmt.Errorf("poll: watchlist[%d] has invalid videoFormat %d (must be 1–5 or -1 for global)", i, wa.VideoFormat)
		}
	}
	if cfg.Format != 0 && !(cfg.Format >= 1 && cfg.Format <= 5) {
		return fmt.Errorf("poll: format %d is invalid — must be 1–5", cfg.Format)
	}
	if cfg.VideoFormat != 0 && !(cfg.VideoFormat >= 1 && cfg.VideoFormat <= 5) {
		return fmt.Errorf("poll: videoFormat %d is invalid — must be 1–5", cfg.VideoFormat)
	}
	if cfg.StateFilePath == "" {
		return fmt.Errorf("poll: stateFilePath is required — set an absolute path in config.json")
	}
	if !filepath.IsAbs(cfg.StateFilePath) {
		log.Printf("[poll] WARN: stateFilePath %q is a relative path — state and history files will be lost on container restart", cfg.StateFilePath)
	}
	if cfg.NotifyWebhookURL != "" {
		switch cfg.NotifyWebhookType {
		case "discord", "slack", "generic", "":
		default:
			return fmt.Errorf("poll: unknown notifyWebhookType %q — must be discord, slack, or generic", cfg.NotifyWebhookType)
		}
	}
	return nil
}

func runPollMode(dryRun bool, configPath string) {
	cfg, err := readConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to read config.: %v", err)
	}
	cfg.DryRun = dryRun
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

	if cfg.DryRun {
		log.Printf("[poll] *** DRY RUN MODE — no downloads will occur ***")
	}
	fmt.Println("Poll mode — authenticated. Starting watcher.")
	runPoller(cfg, streamParams)
}

func buildHistCache(watchlist []WatchedArtist, stateFilePath string) map[string]bool {
	histDir := filepath.Dir(stateFilePath)
	hs := make(map[string]bool)
	for _, wa := range watchlist {
		artistIntID, err := strconv.Atoi(wa.ArtistID)
		if err != nil {
			continue
		}
		for _, suffix := range []string{historySuffixAudio, historySuffixVideo} {
			hf := getHistoryFileName(artistIntID, suffix, histDir)
			f, err := os.Open(hf)
			if err != nil {
				continue
			}
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				hs[hf+"\x00"+scanner.Text()] = true
			}
			f.Close()
		}
	}
	return hs
}

func runPoller(cfg *Config, streamParams *StreamParams) {
	stateFile := cfg.StateFilePath
	interval := cfg.PollIntervalMins
	if interval <= 0 {
		interval = defaultPollIntervalMins
	}
	histCache := buildHistCache(cfg.Watchlist, cfg.StateFilePath)
	log.Printf("[poll] starting; %d artists, interval: %d min, state: %s",
		len(cfg.Watchlist), interval, stateFile)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	for {
		pollOnce(cfg, streamParams, stateFile, histCache)
		select {
		case <-stop:
			log.Printf("[poll] received shutdown signal — exiting cleanly")
			return
		case <-time.After(time.Duration(interval) * time.Minute):
		}
	}
}

func pollOnce(cfg *Config, streamParams *StreamParams, stateFile string, histCache map[string]bool) {
	state := loadState(stateFile)
	histDir := filepath.Dir(cfg.StateFilePath)

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
			artistCfg.WantRes = resolveRes[artistCfg.VideoFormat]
		}
		if wa.OutPath != "" {
			artistCfg.OutPath = wa.OutPath
		}
		artistIntID, atoiErr := strconv.Atoi(wa.ArtistID)
		if atoiErr != nil {
			log.Printf("[poll] WARN: artist %q has non-numeric ArtistID %q — skipping histCache check", wa.Name, wa.ArtistID)
			artistIntID = -1
		}

		// Detect first backfill cycle: backfillAll=true with no prior state for this artist.
		// Checked before any containers are marked known so the snapshot is accurate.
		isBackfillCycle := wa.BackfillAll && len(state[wa.ArtistID]) == 0

		knownSet := buildKnownSet(state[wa.ArtistID])
		for _, meta := range containers {
			for _, container := range meta.Response.Containers {
				if knownSet[container.ContainerID] {
					continue
				}
				albumIDStr := strconv.Itoa(container.ContainerID)

				// Skip containers already recorded in the history cache (guards state-reset edge case)
				alreadyInHist := false
				for _, suffix := range []string{historySuffixAudio, historySuffixVideo} {
					if histCache[getHistoryFileName(artistIntID, suffix, histDir)+"\x00"+albumIDStr] {
						alreadyInHist = true
						break
					}
				}
				if alreadyInHist {
					markKnown(state, wa.ArtistID, container.ContainerID)
					knownSet[container.ContainerID] = true
					if err := saveState(stateFile, state); err != nil {
						log.Printf("[poll] ERROR saving state: %v", err)
					}
					continue
				}

				log.Printf("[poll] NEW release: %s — %s (ID: %s)", wa.Name, container.ContainerInfo, albumIDStr)

				if cfg.DryRun {
					log.Printf("[poll] DRY RUN — would download: %s container %d (%s)", wa.Name, container.ContainerID, container.ContainerInfo)
					markKnown(state, wa.ArtistID, container.ContainerID)
					knownSet[container.ContainerID] = true
					if err := saveState(stateFile, state); err != nil {
						log.Printf("[poll] ERROR saving state: %v", err)
					}
					continue
				}

				// album() re-fetches full metadata when albumID is non-empty (correct path for tracks)
				if err := album(albumIDStr, &artistCfg, streamParams, nil); err != nil {
					log.Printf("[poll] ERROR downloading %s: %v", albumIDStr, err)
					continue
				}

				// Update in-memory history cache so subsequent cycles don't re-check disk
				for _, suffix := range []string{historySuffixAudio, historySuffixVideo} {
					histCache[getHistoryFileName(artistIntID, suffix, histDir)+"\x00"+albumIDStr] = true
				}

				// State written per successful download (crash safety — ARCHITECTURE invariant)
				markKnown(state, wa.ArtistID, container.ContainerID)
				knownSet[container.ContainerID] = true
				if err := saveState(stateFile, state); err != nil {
					log.Printf("[poll] ERROR saving state: %v", err)
				}

				if !isBackfillCycle || cfg.NotifyOnBackfill {
					msg := fmt.Sprintf("New release from %s: %s", wa.Name, container.ContainerInfo)
					if notifyErr := sendNotification(cfg.NotifyWebhookURL, cfg.NotifyWebhookType, msg); notifyErr != nil {
						log.Printf("[poll] ERROR sending notification: %v", notifyErr)
					}
				}
			}
		}
		if cfg.ArtistCheckDelaySecs > 0 {
			time.Sleep(time.Duration(cfg.ArtistCheckDelaySecs) * time.Second)
		}
	}
}
