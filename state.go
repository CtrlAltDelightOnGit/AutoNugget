package main

import (
	"encoding/json"
	"os"
)

// PollState tracks which container IDs have been seen per artist.
type PollState map[string][]int // artistId -> []containerID

func loadState(path string) PollState {
	f, err := os.ReadFile(path)
	if err != nil {
		return make(PollState)
	}
	var s PollState
	if err := json.Unmarshal(f, &s); err != nil {
		return make(PollState)
	}
	return s
}

func saveState(path string, s PollState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func buildKnownSet(ids []int) map[int]bool {
	set := make(map[int]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func markKnown(s PollState, artistID string, containerID int) {
	s[artistID] = append(s[artistID], containerID)
}
