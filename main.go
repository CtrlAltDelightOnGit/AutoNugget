package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"strings"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/dustin/go-humanize"
	"github.com/grafov/m3u8"
)

const (
	devKey         = "x7f54tgbdyc64y656thy47er4"
	clientId       = "Eg7HuH873H65r5rt325UytR5429"
	layout         = "01/02/2006 15:04:05"
	userAgent      = "NugsNet/3.26.724 (Android; 7.1.2; Asus; ASUS_Z01QD; Scale/2.0; en)"
	userAgentTwo   = "nugsnetAndroid"
	authUrl        = "https://id.nugs.net/connect/token"
	streamApiBase  = "https://streamapi.nugs.net/"
	subInfoUrl     = "https://subscriptions.nugs.net/api/v1/me/subscriptions"
	userInfoUrl    = "https://id.nugs.net/connect/userinfo"
	playerUrl      = "https://play.nugs.net/"
	sanRegexStr    = `[\/:*?"><|]`
	chapsFileFname = "chapters_nugs_dl_tmp.txt"
	durRegex       = `Duration: ([\d:.]+)`
	bitrateRegex   = `[\w]+(?:_(\d+)k_v\d+)`
	// history file
	historyFileName      = "history"
	historySuffixAudio     = "Aud"
	historySuffixVideo     = "Vid"
	historyFileExtension = ".txt"
	// folder/filename length limits
	maxFolderNameLen    = 120
	maxVideoFilenameLen = 110
	// poll defaults
	defaultStateFile        = "auto_nugget_state.json"
	defaultPollIntervalMins = 60
)

var (
	jar, _               = cookiejar.New(nil)
	client               = &http.Client{Jar: jar, Timeout: 30 * time.Second}
)

var regexStrings = [11]string{
	`^https://play.nugs.net/release/(\d+)$`,
	`^https://play.nugs.net/#/playlists/playlist/(\d+)$`,
	`^https://play.nugs.net/library/playlist/(\d+)$`,
	`(^https://2nu.gs/[a-zA-Z\d]+$)`,
	`^https://play.nugs.net/#/videos/artist/\d+/.+/(\d+)$`,
	`^https://play.nugs.net/artist/(\d+)(?:/albums|/latest|)$`,
	`^https://play.nugs.net/livestream/(\d+)/exclusive$`,
	`^https://play.nugs.net/watch/livestreams/exclusive/(\d+)$`,
	`^https://play.nugs.net/#/my-webcasts/\d+-(\d+)-\d+-\d+$`,
	`^https://www.nugs.net/on/demandware.store/Sites-NugsNet-Site/d`+
		`efault/(?:Stash-QueueVideo|NugsVideo-GetStashVideo)\?([a-zA-Z0-9=%&-]+$)`,
	`^https://play.nugs.net/library/webcast/(\d+)$`,
}

var (
	sanRegex         = regexp.MustCompile(sanRegexStr)
	bitrateRegexComp = regexp.MustCompile(bitrateRegex)
	durRegexComp     = regexp.MustCompile(durRegex)
	compiledRegexes  = func() [11]*regexp.Regexp {
		var r [11]*regexp.Regexp
		for i, s := range regexStrings {
			r[i] = regexp.MustCompile(s)
		}
		return r
	}()
)

var qualityMap = map[string]Quality{
	".alac16/": {Specs: "16-bit / 44.1 kHz ALAC", Extension: ".m4a", Format: 1},
	".flac16/": {Specs: "16-bit / 44.1 kHz FLAC", Extension: ".flac", Format: 2},
	// .mqa24/ must be above .flac?
	".mqa24/":  {Specs: "24-bit / 48 kHz MQA", Extension: ".flac", Format: 3},
	".flac?": {Specs: "FLAC", Extension: ".flac", Format: 2},
	".s360/":   {Specs: "360 Reality Audio", Extension: ".mp4", Format: 4},
	".aac150/": {Specs: "150 Kbps AAC", Extension: ".m4a", Format: 5},
	".m4a?": {Specs: "AAC", Extension: ".m4a", Format: 5},
	".m3u8?":	{Extension: ".m4a", Format: 6},
}

var resolveRes = map[int]string{
	1: "480",
	2: "720",
	3: "1080",
	4: "1440",
	5: "2160",
}

var trackFallback = map[int]int{
	1: 2,
	2: 5,
	3: 2,
	4: 3,
}

var resFallback = map[string]string{
	"720":  "480",
	"1080": "720",
	"1440": "1080",
}

func (wc *WriteCounter) Write(p []byte) (int, error) {
	var speed int64 = 0
	n := len(p)
	wc.Downloaded += int64(n)
	percentage := float64(wc.Downloaded) / float64(wc.Total) * float64(100)
	wc.Percentage = int(percentage)
	toDivideBy := time.Now().UnixMilli() - wc.StartTime
	if toDivideBy != 0 {
		speed = int64(wc.Downloaded) / toDivideBy * 1000
	}
	fmt.Printf("\r%d%% @ %s/s, %s/%s ", wc.Percentage, humanize.Bytes(uint64(speed)),
		humanize.Bytes(uint64(wc.Downloaded)), wc.TotalStr)
	return n, nil
}

func wasRunFromSrc() bool {
	buildPath := filepath.Join(os.TempDir(), "go-build")
	return strings.HasPrefix(os.Args[0], buildPath)
}

func getScriptDir() (string, error) {
	var (
		ok    bool
		err   error
		fname string
	)
	runFromSrc := wasRunFromSrc()
	if runFromSrc {
		_, fname, _, ok = runtime.Caller(0)
		if !ok {
			return "", errors.New("failed to get script filename")
		}
	} else {
		fname, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	return filepath.Dir(fname), nil
}

func readTxtFile(path string) ([]string, error) {
	var lines []string
	f, err := os.OpenFile(path, os.O_RDONLY, 0755)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if scanner.Err() != nil {
		return nil, scanner.Err()
	}
	return lines, nil
}

func contains(lines []string, value string) bool {
	for _, line := range lines {
		if strings.EqualFold(line, value) {
			return true
		}
	}
	return false
}

func processUrls(urls []string) ([]string, error) {
	var (
		processed []string
		txtPaths  []string
	)
	for _, _url := range urls {
		if strings.HasSuffix(_url, ".txt") && !contains(txtPaths, _url) {
			txtLines, err := readTxtFile(_url)
			if err != nil {
				return nil, err
			}
			for _, txtLine := range txtLines {
				if !contains(processed, txtLine) {
					txtLine = strings.TrimSuffix(txtLine, "/")
					processed = append(processed, txtLine)
				}
			}
			txtPaths = append(txtPaths, _url)
		} else {
			if !contains(processed, _url) {
				_url = strings.TrimSuffix(_url, "/")
				processed = append(processed, _url)
			}
		}
	}
	return processed, nil
}

func parseCfg(args *Args) (*Config, error) {
	cfg, err := readConfig()
	if err != nil {
		return nil, err
	}
	if args.Format != -1 {
		cfg.Format = args.Format
	}
	if args.VideoFormat != -1 {
		cfg.VideoFormat = args.VideoFormat
	}
	if !(cfg.Format >= 1 && cfg.Format <= 5) {
		return nil, errors.New("track Format must be between 1 and 5")
	}
	if !(cfg.VideoFormat >= 1 && cfg.VideoFormat <= 5) {
		return nil, errors.New("video format must be between 1 and 5")
	}
	cfg.WantRes = resolveRes[cfg.VideoFormat]
	if args.OutPath != "" {
		cfg.OutPath = args.OutPath
	}
	if cfg.OutPath == "" {
		cfg.OutPath = "Nugs downloads"
	}
	if cfg.Token != "" {
		cfg.Token = strings.TrimPrefix(cfg.Token, "Bearer ")
	}
	if cfg.UseFfmpegEnvVar {
		cfg.FfmpegNameStr = "ffmpeg"
	} else {
		cfg.FfmpegNameStr = "./ffmpeg"
	}
	cfg.Urls, err = processUrls(args.Urls)
	if err != nil {
		fmt.Println("Failed to process URLs.")
		return nil, err
	}
	cfg.ForceVideo = args.ForceVideo

	if args.SkipVideos == true || args.AudioOnly == true{
		cfg.SkipVideos = true
	}

	if args.VideoOnly == true {
		cfg.VideoOnly = true
		cfg.ForceVideo = true
	}

	cfg.SkipChapters = args.SkipChapters
	return cfg, nil
}

func readConfig() (*Config, error) {
	data, err := os.ReadFile("config.json")
	if err != nil {
		return nil, err
	}
	var obj Config
	err = json.Unmarshal(data, &obj)
	if err != nil {
		return nil, err
	}
	return &obj, nil
}

func makeDirs(path string) error {
	err := os.MkdirAll(path, 0755)
	return err
}

func fileExists(path string) (bool, error) {
	f, err := os.Stat(path)
	if err == nil {
		return !f.IsDir(), nil
	} else if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func sanitise(filename string) string {
	san := sanRegex.ReplaceAllString(filename, "_")
	return strings.TrimSuffix(san, "\t")
}

func auth(email, pwd string) (string, error) {
	data := url.Values{}
	data.Set("client_id", clientId)
	data.Set("grant_type", "password")
	data.Set("scope", "openid profile email nugsnet:api nugsnet:legacyapi offline_access")
	data.Set("username", email)
	data.Set("password", pwd)
	req, err := http.NewRequest(http.MethodPost, authUrl, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Add("User-Agent", userAgent)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	do, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK {
		return "", errors.New(do.Status)
	}
	var obj Auth
	err = json.NewDecoder(do.Body).Decode(&obj)
	if err != nil {
		return "", err
	}
	return obj.AccessToken, nil
}

func apiGet(rawURL, token, ua string, target interface{}) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Add("User-Agent", ua)
	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	do, err := client.Do(req)
	if err != nil {
		return err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK {
		return errors.New(do.Status)
	}
	return json.NewDecoder(do.Body).Decode(target)
}

func getUserInfo(token string) (string, error) {
	var obj UserInfo
	if err := apiGet(userInfoUrl, token, userAgent, &obj); err != nil {
		return "", err
	}
	return obj.Sub, nil
}

func getSubInfo(token string) (*SubInfo, error) {
	var obj SubInfo
	if err := apiGet(subInfoUrl, token, userAgent, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func getPlan(subInfo *SubInfo) (string, bool) {
	if subInfo.Plan.ID != "" {
		return subInfo.Plan.Description, false
	} else {
		return subInfo.Promo.Plan.Description, true
	}
}

func parseTimestamps(start, end string) (string, string) {
	startTime, _ := time.Parse(layout, start)
	endTime, _ := time.Parse(layout, end)
	parsedStart := strconv.FormatInt(startTime.Unix(), 10)
	parsedEnd := strconv.FormatInt(endTime.Unix(), 10)
	return parsedStart, parsedEnd
}

func parseStreamParams(userId string, subInfo *SubInfo, isPromo bool) *StreamParams {
	startStamp, endStamp := parseTimestamps(subInfo.StartedAt, subInfo.EndsAt)
	streamParams := &StreamParams{
		SubscriptionID:          subInfo.LegacySubscriptionID,
		SubCostplanIDAccessList: subInfo.Plan.PlanID,
		UserID:                  userId,
		StartStamp:              startStamp,
		EndStamp:                endStamp,
	}
	if isPromo {
		streamParams.SubCostplanIDAccessList = subInfo.Promo.Plan.PlanID
	} else {
		streamParams.SubCostplanIDAccessList = subInfo.Plan.PlanID
	}
	return streamParams
}

func checkUrl(_url string) (string, int) {
	for i, regex := range compiledRegexes {
		match := regex.FindStringSubmatch(_url)
		if match != nil {
			return match[1], i
		}
	}
	return "", 0
}

func extractLegToken(tokenStr string) (string, string, error) {
	payload := strings.SplitN(tokenStr, ".", 3)[1]
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", "", err
	}
	var obj Payload
	err = json.Unmarshal(decoded, &obj)
	if err != nil {
		return "", "", err
	}
	return obj.LegacyToken, obj.LegacyUguid, nil
}

func getAlbumMeta(albumId string) (*AlbumMeta, error) {
	query := url.Values{}
	query.Set("method", "catalog.container")
	query.Set("containerID", albumId)
	query.Set("vdisp", "1")
	var obj AlbumMeta
	if err := apiGet(streamApiBase+"api.aspx?"+query.Encode(), "", userAgent, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func getPlistMeta(plistId, email, legacyToken string, cat bool) (*PlistMeta, error) {
	var apiPath string
	if cat {
		apiPath = "api.aspx"
	} else {
		apiPath = "secureApi.aspx"
	}
	query := url.Values{}
	if cat {
		query.Set("method", "catalog.playlist")
		query.Set("plGUID", plistId)
	} else {
		query.Set("method", "user.playlist")
		query.Set("playlistID", plistId)
		query.Set("developerKey", devKey)
		query.Set("user", email)
		query.Set("token", legacyToken)
	}
	var obj PlistMeta
	if err := apiGet(streamApiBase+apiPath+"?"+query.Encode(), "", userAgentTwo, &obj); err != nil {
		return nil, err
	}
	return &obj, nil
}

func getArtistMeta(artistId string) ([]*ArtistMeta, error) {
	var allArtistMeta []*ArtistMeta
	offset := 1
	query := url.Values{}
	query.Set("method", "catalog.containersAll")
	query.Set("limit", "100")
	query.Set("artistList", artistId)
	query.Set("availType", "1")
	query.Set("vdisp", "1")
	for {
		query.Set("startOffset", strconv.Itoa(offset))
		var obj ArtistMeta
		if err := apiGet(streamApiBase+"api.aspx?"+query.Encode(), "", userAgent, &obj); err != nil {
			return nil, err
		}
		retLen := len(obj.Response.Containers)
		if retLen == 0 {
			break
		}
		allArtistMeta = append(allArtistMeta, &obj)
		offset += retLen
	}
	return allArtistMeta, nil
}

func getPurchasedManUrl(skuID int, showID, userID, uguID string) (string, error) {
	query := url.Values{}
	query.Set("skuId", strconv.Itoa(skuID))
	query.Set("showId", showID)
	query.Set("uguid", uguID)
	query.Set("nn_userID", userID)
	query.Set("app", "1")
	var obj PurchasedManResp
	if err := apiGet(streamApiBase+"bigriver/vidPlayer.aspx?"+query.Encode(), "", userAgentTwo, &obj); err != nil {
		return "", err
	}
	return obj.FileURL, nil
}

func getStreamMeta(trackId, skuId, format int, streamParams *StreamParams) (string, error) {
	query := url.Values{}
	if format == 0 {
		query.Set("skuId", strconv.Itoa(skuId))
		query.Set("containerID", strconv.Itoa(trackId))
		query.Set("chap", "1")
	} else {
		query.Set("platformID", strconv.Itoa(format))
		query.Set("trackID", strconv.Itoa(trackId))
	}
	query.Set("app", "1")
	query.Set("subscriptionID", streamParams.SubscriptionID)
	query.Set("subCostplanIDAccessList", streamParams.SubCostplanIDAccessList)
	query.Set("nn_userID", streamParams.UserID)
	query.Set("startDateStamp", streamParams.StartStamp)
	query.Set("endDateStamp", streamParams.EndStamp)
	var obj StreamMeta
	if err := apiGet(streamApiBase+"bigriver/subPlayer.aspx?"+query.Encode(), "", userAgentTwo, &obj); err != nil {
		return "", err
	}
	return obj.StreamLink, nil
}

func queryQuality(streamUrl string) *Quality {
	for k, v := range qualityMap {
		if strings.Contains(streamUrl, k) {
			v.URL = streamUrl
			return &v
		}
	}
	return nil
}

func downloadTrack(trackPath, _url string) error {
	f, err := os.OpenFile(trackPath, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequest(http.MethodGet, _url, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Referer", playerUrl)
	req.Header.Add("User-Agent", userAgent)
	req.Header.Add("Range", "bytes=0-")
	do, err := client.Do(req)
	if err != nil {
		return err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK && do.StatusCode != http.StatusPartialContent {
		return errors.New(do.Status)
	}
	totalBytes := do.ContentLength
	counter := &WriteCounter{
		Total:     totalBytes,
		TotalStr:  humanize.Bytes(uint64(totalBytes)),
		StartTime: time.Now().UnixMilli(),
	}
	_, err = io.Copy(f, io.TeeReader(do.Body, counter))
	fmt.Println("")
	return err
}

func getTrackQual(quals []*Quality, wantFmt int) *Quality {
	for _, quality := range quals {
		if quality.Format == wantFmt {
			return quality
		}
	}
	return nil
}

func extractBitrate(manUrl string) string {
	match := bitrateRegexComp.FindStringSubmatch(manUrl)
	if match != nil {
		return match[1]
	}
	return ""
}

func parseHlsMaster(qual *Quality) error {
	req, err := client.Get(qual.URL)
	if err != nil {
		return err
	}
	defer req.Body.Close()
	if req.StatusCode != http.StatusOK {
		return errors.New(req.Status)
	}

	playlist, _, err := m3u8.DecodeFrom(req.Body, true)
	if err != nil {
		return err
	}
	master := playlist.(*m3u8.MasterPlaylist)
	sort.Slice(master.Variants, func(x, y int) bool {
		return master.Variants[x].Bandwidth > master.Variants[y].Bandwidth
	})
	variantUri := master.Variants[0].URI
	bitrate := extractBitrate(variantUri)
	if bitrate == "" {
		return errors.New("no regex match for manifest bitrate")
	}
	qual.Specs = bitrate + " Kbps AAC"
	manBase, q, err := getManifestBase(qual.URL)
	if err != nil {
		return err
	}
	qual.URL = manBase + variantUri + q
	return nil
}
func getKey(keyUrl string) ([]byte, error) {
	req, err := client.Get(keyUrl)
	if err != nil {
		return nil, err
	}
	defer req.Body.Close()
	if req.StatusCode != http.StatusOK {
		return nil, errors.New(req.Status)
	}
	buf := make([]byte, 16)
	_, err = io.ReadFull(req.Body, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}


func pkcs5Trimming(data []byte) []byte {
	padding := data[len(data)-1]
	return data[:len(data)-int(padding)]
}

func decryptTrack(key, iv []byte) ([]byte, error) {
	encData, err := os.ReadFile("temp_enc.ts")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ecb := cipher.NewCBCDecrypter(block, iv)
	decrypted := make([]byte, len(encData))
	fmt.Println("Decrypting...")
	ecb.CryptBlocks(decrypted, encData)
	return decrypted, nil
}

func tsToAac(decData []byte, outPath, ffmpegNameStr string) error {
	var errBuffer bytes.Buffer
	cmd := exec.Command(ffmpegNameStr, "-i", "pipe:", "-c:a", "copy", outPath)
	cmd.Stdin = bytes.NewReader(decData)
	cmd.Stderr = &errBuffer
	err := cmd.Run()
	if err != nil {
		errString := fmt.Sprintf("%s\n%s", err, errBuffer.String())
		return errors.New(errString)
	}
	return nil
}


func hlsOnly(trackPath, manUrl, ffmpegNameStr string) error {
	req, err := client.Get(manUrl)
	if err != nil {
		return err
	}
	defer req.Body.Close()
	if req.StatusCode != http.StatusOK {
		return errors.New(req.Status)
	}
	playlist, _, err := m3u8.DecodeFrom(req.Body, true)
	if err != nil {
		return err
	}
	media := playlist.(*m3u8.MediaPlaylist)

	manBase, q, err := getManifestBase(manUrl)
	if err != nil {
		return err
	}
	tsUrl := manBase + media.Segments[0].URI + q

	key := media.Key
	keyBytes, err := getKey(manBase + key.URI)
	if err != nil {
		return err
	}

	iv, err := hex.DecodeString(key.IV[2:])
	if err != nil {
		return err
	}

	err = downloadTrack("temp_enc.ts", tsUrl)
	if err != nil {
		return err
	}
	decData, err := decryptTrack(keyBytes, iv)
	if err != nil {
		return err
	}
	err = os.Remove("temp_enc.ts")
	if err != nil {
		return err
	}
	err = tsToAac(decData, trackPath, ffmpegNameStr)
	return err
}

func checkIfHlsOnly(quals []*Quality) bool {
	for _, quality := range quals {
		if !strings.Contains(quality.URL, ".m3u8?") {
			return false
		}
	}
	return true
}

func processTrack(folPath string, trackNum, trackTotal int, cfg *Config, track *Track, streamParams *StreamParams) error {
	origWantFmt := cfg.Format
	wantFmt := origWantFmt
	var (
		quals      []*Quality
		chosenQual *Quality
	)
	// Call the stream meta endpoint four times to get all avail formats since the formats can shift.
	// This will ensure the right format's always chosen. Probes run concurrently; order is preserved.
	type probeResult struct {
		url string
		err error
	}
	probes := [4]int{1, 4, 7, 10}
	probeResults := make([]probeResult, len(probes))
	var wg sync.WaitGroup
	for i, fmtID := range probes {
		wg.Add(1)
		go func(idx, f int) {
			defer wg.Done()
			u, err := getStreamMeta(track.TrackID, 0, f, streamParams)
			probeResults[idx] = probeResult{u, err}
		}(i, fmtID)
	}
	wg.Wait()
	for _, r := range probeResults {
		if r.err != nil {
			fmt.Println("failed to get track stream metadata")
			return r.err
		}
		if r.url == "" {
			return errors.New("the api didn't return a track stream URL")
		}
		quality := queryQuality(r.url)
		if quality == nil {
			fmt.Println("The API returned an unsupported format, URL:", r.url)
			continue
		}
		quals = append(quals, quality)
	}

	if len(quals) == 0 {
		return errors.New("the api didn't return any formats")
	}

	isHlsOnly := checkIfHlsOnly(quals)

	if isHlsOnly {
		fmt.Println("HLS-only track. Only AAC is available, tags currently unsupported.")
		chosenQual = quals[0]
		err := parseHlsMaster(chosenQual)
		if err != nil {
			return err
		}
	} else {
		for {
			chosenQual = getTrackQual(quals, wantFmt)
			if chosenQual != nil {
				break
			} else {
				// Fallback quality.
				wantFmt = trackFallback[wantFmt]
			}
		}
		if chosenQual == nil {
			return errors.New("no track format was chosen")
		}
		if wantFmt != origWantFmt && origWantFmt != 4 {
			fmt.Println("Unavailable in your chosen format.")
		}
	}
	trackFname := fmt.Sprintf(
		"%02d. %s%s", trackNum, sanitise(track.SongTitle), chosenQual.Extension,
	)
	trackPath := filepath.Join(folPath, trackFname)
	exists, err := fileExists(trackPath)
	if err != nil {
		fmt.Println("Failed to check if track already exists locally.")
		return err
	}
	if exists {
		fmt.Println("Track already exists locally.")
		return nil
	}
	fmt.Printf(
		"Downloading track %d of %d: %s - %s\n", trackNum, trackTotal, track.SongTitle,
		chosenQual.Specs,
	)
	if isHlsOnly {
		err = hlsOnly(trackPath, chosenQual.URL, cfg.FfmpegNameStr)
	} else {
		err = downloadTrack(trackPath, chosenQual.URL)
	}
	if err != nil {
		fmt.Println("Failed to download track.")
		return err
	}
	return nil
}

func album(albumID string, cfg *Config, streamParams *StreamParams, artResp *AlbArtResp) error {
	var (
		meta   *AlbArtResp
		tracks []Track
		historySuffix string
	)
	if albumID == "" {
		meta = artResp
		tracks = meta.Songs
		albumID = strconv.Itoa(artResp.ContainerID)
	} else {
		_meta, err := getAlbumMeta(albumID)
		if err != nil {
			fmt.Println("Failed to get metadata.")
			return err
		}
		meta = _meta.Response
		tracks = meta.Tracks
	}

	trackTotal := len(tracks)

	skuID := getVideoSku(meta.Products)

	if skuID == 0 && trackTotal < 1 {
		return errors.New("release has no tracks or videos")
	}

	historySuffix = historySuffixAudio
	if skuID != 0 {
		historySuffix = historySuffixVideo
	}

	alreadyDownloaded, err := checkHistory(albumID, getHistoryFileName(meta.ArtistID, historySuffix))
	if err != nil {
		fmt.Println("Error checking history:", err)
		return nil
	}

	//todo update the feedback text here
	if alreadyDownloaded {
		fmt.Println("Album already downloaded. Skipping.")
		return nil
	}

	if skuID != 0 {
		if cfg.SkipVideos {
			fmt.Println("Video-only album, skipped.")
			return nil
		}
		if cfg.ForceVideo || trackTotal < 1 {
			// here fec
			return video(albumID, "", cfg, streamParams, meta, false)
		}
	}

	if cfg.VideoOnly == true {
		fmt.Println("Audio only album, skipped.")
		return nil
	}

	albumFolder := meta.ArtistName + " - " + strings.TrimRight(meta.ContainerInfo, " ")
	fmt.Println(albumFolder)
	if len(albumFolder) > maxFolderNameLen {
		albumFolder = albumFolder[:maxFolderNameLen]
		fmt.Println(
			"Album folder name was chopped because it exceeds 120 characters.")
	}
	albumPath := filepath.Join(cfg.OutPath, sanitise(albumFolder))

	err = makeDirs(albumPath)
	if err != nil {
		fmt.Println("Failed to make album folder.")
		return err
	}
	for trackNum, track := range tracks {
		trackNum++
		err := processTrack(
			albumPath, trackNum, trackTotal, cfg, &track, streamParams)
		if err != nil {
			log.Printf("Track failed.: %v", err)
		}
	}

	// append the item to the history
	err = appendToHistory(albumID, getHistoryFileName(meta.ArtistID, historySuffix))
	if err != nil {
		fmt.Println("Error updating history:", err)
		return nil
	}

	return nil
}

func getAlbumTotal(meta []*ArtistMeta) int {
	var total int
	for _, _meta := range meta {
		total += len(_meta.Response.Containers)
	}
	return total
}

func artist(artistId string, cfg *Config, streamParams *StreamParams) error {
	meta, err := getArtistMeta(artistId)
	if err != nil {
		fmt.Println("Failed to get artist metadata.")
		return err
	}
	if len(meta) == 0 {
		return errors.New(
			"The API didn't return any artist metadata.")
	}
	fmt.Println(meta[0].Response.Containers[0].ArtistName)
	albumTotal := getAlbumTotal(meta)
	for _, _meta := range meta {
		for albumNum, container := range _meta.Response.Containers {
			fmt.Printf("Item %d of %d:\n", albumNum+1, albumTotal)
			if cfg.SkipVideos {
				err = album("", cfg, streamParams, container)
			} else {
				// Can't re-use this metadata as it doesn't have any product info for videos.
				err = album(strconv.Itoa(container.ContainerID), cfg, streamParams, nil)
			}
			if err != nil {
				log.Printf("Item failed.: %v", err)
			}
		}
	}
	return nil
}

func playlist(plistId, legacyToken string, cfg *Config, streamParams *StreamParams, cat bool) error {
	_meta, err := getPlistMeta(plistId, cfg.Email, legacyToken, cat)
	if err != nil {
		fmt.Println("Failed to get playlist metadata.")
		return err
	}
	meta := _meta.Response
	plistName := meta.PlayListName
	fmt.Println(plistName)
	if len(plistName) > maxFolderNameLen {
		plistName = plistName[:maxFolderNameLen]
		fmt.Println(
			"Playlist folder name was chopped because it exceeds 120 characters.")
	}
	plistPath := filepath.Join(cfg.OutPath, sanitise(plistName))
	err = makeDirs(plistPath)
	if err != nil {
		fmt.Println("Failed to make playlist folder.")
		return err
	}
	trackTotal := len(meta.Items)
	for trackNum, track := range meta.Items {
		trackNum++
		err := processTrack(
			plistPath, trackNum, trackTotal, cfg, &track.Track, streamParams)
		if err != nil {
			log.Printf("Track failed.: %v", err)
		}
	}
	return nil
}

func getVideoSku(products []Product) int {
	for _, product := range products {
		formatStr := product.FormatStr
		if formatStr == "VIDEO ON DEMAND" || formatStr == "LIVE HD VIDEO" {
			return product.SkuID
		}
	}
	return 0
}

func getLstreamSku(products []*ProductFormatList) int {
	for _, product := range products {
		if product.FormatStr == "LIVE HD VIDEO" {
			return product.SkuID
		}
	}
	return 0
}

func getVidVariant(variants []*m3u8.Variant, wantRes string) *m3u8.Variant {
	for _, variant := range variants {
		if strings.HasSuffix(variant.Resolution, "x"+wantRes) {
			return variant
		}
	}
	return nil
}

func formatRes(res string) string {
	if res == "2160" {
		return "4K"
	} else {
		return res + "p"
	}
}

func chooseVariant(manifestUrl, wantRes string) (*m3u8.Variant, string, error) {
	origWantRes := wantRes
	var wantVariant *m3u8.Variant
	req, err := client.Get(manifestUrl)
	if err != nil {
		return nil, "", err
	}
	defer req.Body.Close()
	if req.StatusCode != http.StatusOK {
		return nil, "", errors.New(req.Status)
	}
	playlist, _, err := m3u8.DecodeFrom(req.Body, true)
	if err != nil {
		return nil, "", err
	}
	master := playlist.(*m3u8.MasterPlaylist)
	sort.Slice(master.Variants, func(x, y int) bool {
		return master.Variants[x].Bandwidth > master.Variants[y].Bandwidth
	})
	if wantRes == "2160" {
		variant := master.Variants[0]
		varRes := strings.SplitN(variant.Resolution, "x", 2)[1]
		varRes = formatRes(varRes)
		return variant, varRes, nil
	}
	for {
		wantVariant = getVidVariant(master.Variants, wantRes)
		if wantVariant != nil {
			break
		} else {
			wantRes = resFallback[wantRes]
		}
	}
	if wantVariant == nil {
		return nil, "", errors.New("No variant was chosen.")
	}
	if wantRes != origWantRes {
		fmt.Println("Unavailable in your chosen format.")
	}
	wantRes = formatRes(wantRes)
	return wantVariant, wantRes, nil
}

func getManifestBase(manifestUrl string) (string, string, error) {
	u, err := url.Parse(manifestUrl)
	if err != nil {
		return "", "", err
	}
	path := u.Path
	lastPathIdx := strings.LastIndex(path, "/")
	base := u.Scheme + "://" + u.Host + path[:lastPathIdx+1]
	return base, "?" + u.RawQuery, nil
}

func getSegUrls(manifestUrl, query string) ([]string, error) {
	var segUrls []string
	req, err := client.Get(manifestUrl)
	if err != nil {
		return nil, err
	}
	defer req.Body.Close()
	if req.StatusCode != http.StatusOK {
		return nil, errors.New(req.Status)
	}
	playlist, _, err := m3u8.DecodeFrom(req.Body, true)
	if err != nil {
		return nil, err
	}
	media := playlist.(*m3u8.MediaPlaylist)
	for _, seg := range media.Segments {
		if seg == nil {
			break
		}
		segUrls = append(segUrls, seg.URI+query)
	}
	return segUrls, nil
}

func downloadVideo(videoPath, _url string) error {
	f, err := os.OpenFile(videoPath, os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}
	startByte := stat.Size()

	req, err := http.NewRequest(http.MethodGet, _url, nil)
	if err != nil {
		return err
	}
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-", startByte))
	do, err := client.Do(req)
	if err != nil {
		return err
	}
	defer do.Body.Close()
	if do.StatusCode != http.StatusOK && do.StatusCode != http.StatusPartialContent {
		return errors.New(do.Status)
	}

	if startByte > 0 {
		fmt.Printf("TS already exists locally, resuming from byte %d...\n", startByte)
		startByte = 0
	}

	totalBytes := do.ContentLength
	counter := &WriteCounter{
		Total:     totalBytes,
		TotalStr:  humanize.Bytes(uint64(totalBytes)),
		StartTime: time.Now().UnixMilli(),
		Downloaded: startByte,
	}
	_, err = io.Copy(f, io.TeeReader(do.Body, counter))
	fmt.Println("")
	return err
}

func downloadLstream(videoPath, baseUrl string, segUrls []string) error {
	f, err := os.OpenFile(videoPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer f.Close()
	segTotal := len(segUrls)
	for segNum, segUrl := range segUrls {
		segNum++
		fmt.Printf("\rSegment %d of %d.", segNum, segTotal)
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodGet, baseUrl+segUrl, nil)
		if err != nil {
			return err
		}
		do, err := client.Do(req)
		if err != nil {
			return err
		}
		if do.StatusCode != http.StatusOK {
			do.Body.Close()
			return errors.New(do.Status)
		}
		_, err = io.Copy(f, do.Body)
		do.Body.Close()
		if err != nil {
			return err
		}
	}
	fmt.Println("")
	return err
}

func extractDuration(errStr string) string {
	match := durRegexComp.FindStringSubmatch(errStr)
	if match != nil {
		return match[1]
	}
	return ""
}

func parseDuration(dur string) (int, error) {
	dur = strings.Replace(dur, ":", "h", 1)
	dur = strings.Replace(dur, ":", "m", 1)
	dur = strings.Replace(dur, ".", "s", 1)
	dur += "ms"
	d, err := time.ParseDuration(dur)
	if err != nil {
		return 0, err
	}
	rounded := math.Round(d.Seconds())
	return int(rounded), nil
}

// Horrible, but best way without ffprobe.
// My native Go duration calculation's too slow. Is there a way without having to iterate over all the packets?
func getDuration(tsPath, ffmpegNameStr string) (int, error) {
	var errBuffer bytes.Buffer
	args := []string{"-hide_banner", "-i", tsPath}
	cmd := exec.Command(ffmpegNameStr, args...)
	cmd.Stderr = &errBuffer
	// Return code's always 1 as we're not providing any output files.
	err := cmd.Run()
	if err.Error() != "exit status 1" {
		return 0, err
	}
	errStr := errBuffer.String()
	ok := strings.HasSuffix(
		strings.TrimSpace(errStr), "At least one output file must be specified")
	if !ok {
		errString := fmt.Sprintf("%s\n%s", err, errStr)
		return 0, errors.New(errString)
	}
	dur := extractDuration(errStr)
	if dur == "" {
		return 0, errors.New("No regex match.")
	}
	durSecs, err := parseDuration(dur)
	if err != nil {
		return 0, err
	}
	return durSecs, nil
}

func getNextChapStart(chapters []interface{}, idx int) float64 {
	for i, chapter := range chapters {
		if i == idx {
			m := chapter.(map[string]interface{})
			return m["chapterSeconds"].(float64)
		}
	}
	return 0
}


func writeChapsFile(chapters []interface{}, dur int) error {
	f, err := os.OpenFile(chapsFileFname, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(";FFMETADATA1\n")
	if err != nil {
		return err
	}
	chaptersCount := len(chapters)

	var nextChapStart float64

	for i, chapter := range chapters {
		i++
		isLast := i == chaptersCount

		// casting to struct won't work.
		m := chapter.(map[string]interface{})
		start := m["chapterSeconds"].(float64)

		if !isLast {
			nextChapStart = getNextChapStart(chapters, i)
			if nextChapStart <= start {
				continue
			}
		}

		_, err := f.WriteString("\n[CHAPTER]\n")
		if err != nil {
			return err
		}
		_, err = f.WriteString("TIMEBASE=1/1\n")
		if err != nil {
			return err
		}

		startLine := fmt.Sprintf("START=%d\n", int(math.Round(start)))
		_, err = f.WriteString(startLine)
		if err != nil {
			return err
		}
		if isLast {
			endLine := fmt.Sprintf("END=%d\n", dur)
			_, err = f.WriteString(endLine)
			if err != nil {
				return err
			}
		} else {
			endLine := fmt.Sprintf("END=%d\n", int(math.Round(nextChapStart)-1))
			_, err = f.WriteString(endLine)
			if err != nil {
				return err
			}
		}
		_, err = f.WriteString("TITLE=" + m["chaptername"].(string) + "\n")
		if err != nil {
			return err
		}
	}
	return nil
}

// There are native MPEG demuxers and MP4 muxers for Go, but they're too slow.
func tsToMp4(VidPathTs, vidPath, ffmpegNameStr string, chapAvail bool) error {
	var (
		errBuffer bytes.Buffer
		args      []string
	)
	if chapAvail {
		args = []string{
			"-hide_banner", "-i", VidPathTs, "-f", "ffmetadata",
			"-i", chapsFileFname, "-map_metadata", "1", "-c", "copy", vidPath,
		}
	} else {
		args = []string{"-hide_banner", "-i", VidPathTs, "-c", "copy", vidPath}
	}
	cmd := exec.Command(ffmpegNameStr, args...)
	cmd.Stderr = &errBuffer
	err := cmd.Run()
	if err != nil {
		errString := fmt.Sprintf("%s\n%s", err, errBuffer.String())
		return errors.New(errString)
	}
	return nil
}

func getLstreamContainer(containers []*AlbArtResp) *AlbArtResp {
	for i := len(containers) - 1; i >= 0; i-- {
		c := containers[i]
		if c.AvailabilityTypeStr == "AVAILABLE" && c.ContainerTypeStr == "Show" {
			return c
		}
	}
	return nil
}

func parseLstreamMeta(_meta *ArtistMeta) *AlbumMeta {
	meta := getLstreamContainer(_meta.Response.Containers)
	parsed := &AlbumMeta{
		Response: &AlbArtResp{
			ArtistName:        meta.ArtistName,
			ContainerInfo:     meta.ContainerInfo,
			ContainerID:       meta.ContainerID,
			VideoChapters:     meta.VideoChapters,
			Products:          meta.Products,
			ProductFormatList: meta.ProductFormatList,
		},
	}
	return parsed
}

func video(videoID, uguID string, cfg *Config, streamParams *StreamParams, _meta *AlbArtResp, isLstream bool) error {

	var (
		chapsAvail bool
		skuID int
		manifestUrl string
		meta *AlbArtResp
		err error
	)

	if _meta != nil {
		meta = _meta
	} else {
		m, err := getAlbumMeta(videoID)
		if err != nil {
			fmt.Println("Failed to get metadata.")
			return err
		}
		meta = m.Response
	}

	alreadyDownloaded, err := checkHistory(videoID, getHistoryFileName(meta.ArtistID, historySuffixVideo))
	if err != nil {
		fmt.Println("Error checking history:", err)
		return nil
	}

	if alreadyDownloaded {
		fmt.Println("Album already downloaded. Skipping.")
		return nil
	}

	if !cfg.SkipChapters {
		chapsAvail = len(meta.VideoChapters) > 0
	}
	
	videoFname := meta.ArtistName + " - " + strings.TrimRight(meta.ContainerInfo, " ")
	fmt.Println(videoFname)
	if len(videoFname) > maxVideoFilenameLen {
		videoFname = videoFname[:maxVideoFilenameLen]
		fmt.Println(
			"Video filename was chopped because it exceeds 110 characters.")
	}
	if isLstream {
		skuID = getLstreamSku(meta.ProductFormatList)
	} else {
		skuID = getVideoSku(meta.Products)
	}
	if skuID == 0 {
		return errors.New("no video available")
	}
	if uguID == "" {
		manifestUrl, err = getStreamMeta(
			meta.ContainerID, skuID, 0, streamParams)
	} else {
		manifestUrl, err = getPurchasedManUrl(skuID, videoID, streamParams.UserID, uguID)
	}

	if err != nil {
		fmt.Println("Failed to get video file metadata.")
		return err
	} else if manifestUrl == "" {
		return errors.New("the api didn't return a video manifest url")
	}
	variant, retRes, err := chooseVariant(manifestUrl, cfg.WantRes)
	if err != nil {
		fmt.Println("Failed to get video master manifest.")
		return err
	}
	vidPathNoExt := filepath.Join(cfg.OutPath, sanitise(videoFname+"_"+retRes))
	VidPathTs := vidPathNoExt + ".ts"
	vidPath := vidPathNoExt + ".mp4"
	exists, err := fileExists(vidPath)
	if err != nil {
		fmt.Println("Failed to check if video already exists locally.")
		return err
	}
	if exists {
		fmt.Println("Video already exists locally.")
		return nil
	}
	manBaseUrl, query, err := getManifestBase(manifestUrl)
	if err != nil {
		fmt.Println("Failed to get video manifest base URL.")
		return err
	}

	segUrls, err := getSegUrls(manBaseUrl+variant.URI, query)
	if err != nil {
		fmt.Println("Failed to get video segment URLs.")
		return err
	}

	// Player album page videos aren't always only the first seg for the entire vid.
	isLstream = segUrls[0] != segUrls[1]

	if !isLstream {
		fmt.Printf("%.3f FPS, ", variant.FrameRate)
	}
	fmt.Printf("%d Kbps, %s (%s)\n",
		variant.Bandwidth/1000, retRes, variant.Resolution)
	if isLstream {
		err = downloadLstream(VidPathTs, manBaseUrl, segUrls)
	} else {
		err = downloadVideo(VidPathTs, manBaseUrl+segUrls[0])
	}
	if err != nil {
		fmt.Println("Failed to download video segments.")
		return err
	}
	if chapsAvail {
		dur, err := getDuration(VidPathTs, cfg.FfmpegNameStr)
		if err != nil {
			fmt.Println("Failed to get TS duration.")
			return err
		}
		err = writeChapsFile(meta.VideoChapters, dur)
		if err != nil {
			fmt.Println("Failed to write chapters file.")
			return err
		}
	}
	fmt.Println("Putting into MP4 container...")
	err = tsToMp4(VidPathTs, vidPath, cfg.FfmpegNameStr, chapsAvail)
	if err != nil {
		fmt.Println("Failed to put TS into MP4 container.")
		return err
	}
	if chapsAvail {
		err = os.Remove(chapsFileFname)
		if err != nil {
			fmt.Println("Failed to delete chapters file.")
		}
	}
	err = os.Remove(VidPathTs)
	if err != nil {
		fmt.Println("Failed to delete TS.")
	}

	// append the item to the history
	err = appendToHistory(videoID, getHistoryFileName(meta.ArtistID, historySuffixVideo))
	if err != nil {
		fmt.Println("Error updating history:", err)
		return nil
	}

	return nil
}

func resolveCatPlistId(plistUrl string) (string, error) {
	req, err := client.Get(plistUrl)
	if err != nil {
		return "", err
	}
	req.Body.Close()
	if req.StatusCode != http.StatusOK {
		return "", errors.New(req.Status)
	}
	location := req.Request.URL.String()
	u, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", err
	}
	resolvedId := q.Get("plGUID")
	if resolvedId == "" {
		return "", errors.New("not a catalog playlist")
	}
	return resolvedId, nil
}

func catalogPlist(_plistId, legacyToken string, cfg *Config, streamParams *StreamParams) error {
	plistId, err := resolveCatPlistId(_plistId)
	if err != nil {
		fmt.Println("Failed to resolve playlist ID.")
		return err
	}
	err = playlist(plistId, legacyToken, cfg, streamParams, true)
	return err
}

func paidLstream(query, uguID string, cfg *Config, streamParams *StreamParams) error {
    q, err := url.ParseQuery(query)
	if err != nil {
		return err
	}
	showId := q["showID"][0]
	if showId == "" {
		return errors.New("url didn't contain a show id parameter")
	}
	err = video(showId, uguID, cfg, streamParams, nil, true)
	return err
}

// appendToHistory adds the URL to the history file.
func appendToHistory(url, historyFile string) error {
	file, err := os.OpenFile(historyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(url + "\n")
	return err
}

// checkHistory checks if the URL is already in the history file.
func checkHistory(url, historyFile string) (bool, error) {
	file, err := os.Open(historyFile)
	if os.IsNotExist(err) {
		return false, nil // No history yet
	} else if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if scanner.Text() == url {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func getHistoryFileName(artistId int, hType string) string {
	return strconv.Itoa(artistId) + "_" + hType + "_" + historyFileName + historyFileExtension
}

func init() {
	fmt.Println(`
 _             _     __ _  _            __    __   ___  _
/_\     _  _  | |_ / _ \| \| |  _  _  / _' |/ _' |/ -_)| |_
/ _ \  | || | |  _|\___/| .' | | || | \__, |\__, |\___/|  _|
/_/ \_\ \__,_||_|       |_|\_|  \__,_| /_/   /_/       |_|
`)
}

// initSession resolves the auth token, fetches user and subscription info, and returns
// a StreamParams ready for download calls. Callers handle any path-specific setup first.
func initSession(cfg *Config) (token string, subInfo *SubInfo, streamParams *StreamParams, err error) {
	if cfg.Token != "" {
		token = strings.TrimPrefix(cfg.Token, "Bearer ")
	} else {
		token, err = auth(cfg.Email, cfg.Password)
		if err != nil {
			return "", nil, nil, err
		}
	}
	userId, err := getUserInfo(token)
	if err != nil {
		return "", nil, nil, err
	}
	subInfo, err = getSubInfo(token)
	if err != nil {
		return "", nil, nil, err
	}
	_, isPromo := getPlan(subInfo)
	streamParams = parseStreamParams(userId, subInfo, isPromo)
	return token, subInfo, streamParams, nil
}

func main() {
	scriptDir, err := getScriptDir()
	if err != nil {
		panic(err)
	}
	err = os.Chdir(scriptDir)
	if err != nil {
		panic(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "poll" {
		var pollCmd PollCmd
		p, err := arg.NewParser(arg.Config{Out: os.Stdout}, &pollCmd)
		if err != nil {
			log.Fatalf("poll: failed to create parser: %v", err)
		}
		if parseErr := p.Parse(os.Args[2:]); parseErr != nil {
			switch parseErr {
			case arg.ErrHelp:
				p.WriteHelp(os.Stdout)
				os.Exit(0)
			default:
				log.Fatalf("poll: %v", parseErr)
			}
		}
		runPollMode(pollCmd.DryRun)
		return
	}
	var args Args
	arg.MustParse(&args)
	cfg, err := parseCfg(&args)
	if err != nil {
		log.Fatalf("Failed to parse config/args.: %v", err)
	}
	err = makeDirs(cfg.OutPath)
	if err != nil {
		log.Fatalf("Failed to make output folder.: %v", err)
	}
	token, subInfo, streamParams, err := initSession(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize session: %v", err)
	}
	legacyToken, uguID, err := extractLegToken(token)
	if err != nil {
		log.Fatalf("Failed to extract legacy token.: %v", err)
	}
	planDesc, _ := getPlan(subInfo)
	if !subInfo.IsContentAccessible {
		planDesc = "no active subscription"
	}
	fmt.Println(
		"Signed in successfully - " + planDesc + "\n",
	)
	albumTotal := len(cfg.Urls)
	var itemErr error

	for albumNum, _url := range cfg.Urls {

		fmt.Printf("Item %d of %d:\n", albumNum+1, albumTotal)
		itemId, mediaType := checkUrl(_url)

		if itemId == "" {
			fmt.Println("Invalid URL:", _url)
			continue
		}

		switch mediaType {
		case 0:
			itemErr = album(itemId, cfg, streamParams, nil)
		case 1, 2:
			itemErr = playlist(itemId, legacyToken, cfg, streamParams, false)
		case 3:
			itemErr = catalogPlist(itemId, legacyToken, cfg, streamParams)
		case 4, 10:
			itemErr = video(itemId, "", cfg, streamParams, nil, false)
		case 5:
			itemErr = artist(itemId, cfg, streamParams)
		case 6, 7, 8:
			itemErr = video(itemId, "", cfg, streamParams, nil, true)
		case 9:
			itemErr = paidLstream(itemId, uguID, cfg, streamParams)
		}
		if itemErr != nil {
			log.Printf("Item failed.: %v", itemErr)
			continue
		}
	}
}
