// This Source Code Form is subject to the terms of the Lyra Public License,
// v1.0. If a copy of the Lyra Public License was not distributed with this
// file, You can obtain one here:
// www.meshiplaw.com/lyra.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RafaeloxMC/richer-go/client"
)

type ImageUploader string

const (
	UploaderNone      ImageUploader = "none"
	UploaderLitterbox ImageUploader = "litterbox"
	UploaderImgur     ImageUploader = "imgur"
)

type ImageConfig struct {
	Uploader      ImageUploader `json:"uploader"`
	ImgurClientID string        `json:"imgur_client_id"`
}

type Config struct {
	BaseURL         string      `json:"base_url"`
	AuthToken       string      `json:"auth_token"`
	PollIntervalSec int         `json:"poll_interval_sec"`
	Images          ImageConfig `json:"images"`
}

var config = Config{
	BaseURL:         "http://localhost:4746",
	PollIntervalSec: 5,
	Images:          ImageConfig{Uploader: UploaderNone},
}

func loadConfig(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(&config)
}

type Playback struct {
	PlaybackSessionID   string `json:"playback_session_id"`
	TrackID             string `json:"track_id"`
	UserID              string `json:"user_id"`
	PositionMs          int64  `json:"position_ms"`
	EffectivePositionMs int64  `json:"effective_position_ms"`
	State               string `json:"state"`
	ActivityMs          int64  `json:"activity_ms"`
	UpdatedAtMs         int64  `json:"updated_at_ms"`
	DurationMs          *int64 `json:"duration_ms"`
}

type Artist struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Credit *ArtistCredit `json:"credit"`
}

type ArtistCredit struct {
	Type   string  `json:"type"`
	Detail *string `json:"detail"`
	Source string  `json:"source"`
}

type Release struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	ReleaseDate *string `json:"release_date"`
}

type Track struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Artists  []Artist  `json:"artists"`
	Releases []Release `json:"releases"`
}

var coverCache = map[string]string{}
var missingCoverCache = map[string]bool{}

func lyraGet(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", strings.TrimRight(config.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if config.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+config.AuthToken)
	}
	return http.DefaultClient.Do(req)
}

func uploadCover(releaseID string) (string, error) {
	if config.Images.Uploader == UploaderNone {
		return "", fmt.Errorf("image uploads disabled")
	}

	if url, ok := coverCache[releaseID]; ok {
		return url, nil
	}
	if missingCoverCache[releaseID] {
		return "", nil
	}

	resp, err := lyraGet(fmt.Sprintf("/api/releases/%s/cover", url.PathEscape(releaseID)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		missingCoverCache[releaseID] = true
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cover API returned status %d", resp.StatusCode)
	}

	var imageData bytes.Buffer
	if _, err := io.Copy(&imageData, resp.Body); err != nil {
		return "", err
	}

	var url string
	switch config.Images.Uploader {
	case UploaderImgur:
		url, err = uploadToImgur(&imageData)
	default:
		url, err = uploadToLitterbox(&imageData)
	}
	if err != nil {
		return "", err
	}

	coverCache[releaseID] = url
	return url, nil
}

func uploadToLitterbox(image *bytes.Buffer) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("reqtype", "fileupload")
	writer.WriteField("time", "72h")

	part, err := writer.CreateFormFile("fileToUpload", "cover.jpg")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, image); err != nil {
		return "", err
	}
	writer.Close()

	resp, err := http.Post(
		"https://litterbox.catbox.moe/resources/internals/api.php",
		writer.FormDataContentType(),
		&body,
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	urlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(urlBytes)), nil
}

func uploadToImgur(image *bytes.Buffer) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("type", "file")

	part, err := writer.CreateFormFile("image", "cover.jpg")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, image); err != nil {
		return "", err
	}
	writer.Close()

	req, err := http.NewRequest("POST", "https://api.imgur.com/3/image", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Client-ID "+config.Images.ImgurClientID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("imgur API returned status %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.Data.Link, nil
}

func fetchActivePlayback() (*Playback, error) {
	playbacks, status, err := fetchPlaybackSessions("/api/playback-sessions/active")
	if err != nil {
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			return nil, err
		}

		playbacks, _, err = fetchPlaybackSessions("/api/playback-sessions?active=true")
		if err != nil {
			return nil, err
		}
	}

	if len(playbacks) == 0 {
		return nil, nil
	}
	return &playbacks[0], nil
}

func fetchPlaybackSessions(path string) ([]Playback, int, error) {
	resp, err := lyraGet(path)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("playback sessions API returned status %d", resp.StatusCode)
	}

	var result []Playback
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}

	return result, resp.StatusCode, nil
}

func fetchTrack(id string) (*Track, error) {
	query := url.Values{"inc": []string{"releases", "artists"}}
	resp, err := lyraGet(fmt.Sprintf("/api/tracks/%s?%s", url.PathEscape(id), query.Encode()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracks API returned status %d", resp.StatusCode)
	}

	var result Track
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func releaseYear(release Release) string {
	if release.ReleaseDate == nil || len(*release.ReleaseDate) < 4 {
		return ""
	}

	year := (*release.ReleaseDate)[:4]
	for _, r := range year {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return year
}

func displayArtistNames(artists []Artist) []string {
	names := filteredArtistNames(artists, true)
	if len(names) > 0 {
		return names
	}
	return filteredArtistNames(artists, false)
}

func filteredArtistNames(artists []Artist, primaryOnly bool) []string {
	names := make([]string, 0, len(artists))
	seen := make(map[string]bool, len(artists))
	for _, artist := range artists {
		if artist.Name == "" {
			continue
		}
		if primaryOnly && (artist.Credit == nil || artist.Credit.Type != "artist") {
			continue
		}
		if seen[artist.Name] {
			continue
		}
		seen[artist.Name] = true
		names = append(names, artist.Name)
	}
	return names
}

func main() {
	if err := loadConfig("config.json"); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Error loading config: %v", err)
		}
	}

	if config.Images.Uploader == UploaderImgur && config.Images.ImgurClientID == "" {
		log.Fatal("images.imgur_client_id is required when images.uploader is set to \"imgur\"")
	}

	err := client.Login("1474543583473176846")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Logout()

	log.Println("Rich presence is running. Press Ctrl+C to exit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	var lastTrackID string
	var lastState string
	var lastPositionMs int64
	var cachedTrack *Track
	var cachedImage string
	var playbackFetchFailed bool

	ticker := time.NewTicker(time.Duration(config.PollIntervalSec) * time.Second)
	defer ticker.Stop()

	poll := func() {
		playback, err := fetchActivePlayback()
		if err != nil {
			if !playbackFetchFailed {
				log.Printf("Error fetching playback: %v", err)
				playbackFetchFailed = true
			}
			return
		}
		playbackFetchFailed = false
		snapshotNow := time.Now()

		if playback == nil || (playback.State != "playing" && playback.State != "paused") {
			if lastState != "" {
				if err := client.ClearActivity(); err != nil {
					log.Printf("Error clearing activity: %v", err)
				} else {
					log.Println("No active playback, cleared presence.")
				}
			}
			lastTrackID = ""
			lastState = ""
			cachedTrack = nil
			cachedImage = ""
			return
		}

		if playback.TrackID == lastTrackID && playback.State == lastState && playback.PositionMs == lastPositionMs {
			return
		}

		if playback.TrackID != lastTrackID {
			track, err := fetchTrack(playback.TrackID)
			if err != nil {
				log.Printf("Error fetching track: %v", err)
				return
			}
			cachedTrack = track

			cachedImage = "logo-dark"
			if len(track.Releases) > 0 {
				if url, err := uploadCover(track.Releases[0].ID); err != nil {
					log.Printf("Error uploading cover: %v", err)
				} else if url != "" {
					cachedImage = url
				}
			}

			artistNames := displayArtistNames(track.Artists)
			stateLabel := "Playing"
			if playback.State == "paused" {
				stateLabel = "Paused"
			}
			log.Printf("%s: %s - %s", stateLabel, track.Title, strings.Join(artistNames, ", "))
		} else if playback.State != lastState {
			stateLabel := "Playing"
			if playback.State == "paused" {
				stateLabel = "Paused"
			}
			log.Printf("%s: %s", stateLabel, cachedTrack.Title)
		}

		artistNames := displayArtistNames(cachedTrack.Artists)

		activity := client.Activity{
			Type:       client.ActivityListening,
			Details:    cachedTrack.Title,
			LargeImage: cachedImage,
			LargeText:  strings.Join(artistNames, ", "),
		}

		if len(cachedTrack.Releases) > 0 {
			release := cachedTrack.Releases[0]
			if year := releaseYear(release); year != "" {
				activity.State = fmt.Sprintf("%s (%s)", release.Title, year)
			} else {
				activity.State = release.Title
			}
		}

		if playback.State == "playing" {
			// Prefer the server-extrapolated position, which avoids RPC/server clock skew drift.
			effectiveMs := playback.EffectivePositionMs
			if effectiveMs <= 0 {
				effectiveMs = playback.PositionMs
			}
			if playback.DurationMs != nil && effectiveMs > *playback.DurationMs {
				effectiveMs = *playback.DurationMs
			}
			start := snapshotNow.Add(-time.Duration(effectiveMs) * time.Millisecond)
			activity.Timestamps = &client.Timestamps{Start: &start}
			if playback.DurationMs != nil {
				end := start.Add(time.Duration(*playback.DurationMs) * time.Millisecond)
				activity.Timestamps.End = &end
			}
			activity.SmallImage = "playing"
			activity.SmallText = "Playing"
		} else {
			activity.SmallImage = "https://files.catbox.moe/ibpq2d.png"
			activity.SmallText = "Paused"
		}

		if err := client.SetActivity(activity); err != nil {
			log.Printf("Error setting activity: %v", err)
			return
		}

		lastTrackID = playback.TrackID
		lastState = playback.State
		lastPositionMs = playback.PositionMs
	}

	poll()
	for {
		select {
		case <-ticker.C:
			poll()
		case <-sig:
			log.Println("Shutting down.")
			return
		}
	}
}
