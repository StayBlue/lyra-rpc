// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	PollIntervalSec int         `json:"poll_interval_sec"`
	Images          ImageConfig `json:"images"`
}

var config = Config{
	BaseURL:         "http://localhost:3000",
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
	PlaybackID  int64  `json:"playback_id"`
	TrackID     int64  `json:"track_id"`
	UserID      int64  `json:"user_id"`
	PositionMs  int64  `json:"position_ms"`
	State       string `json:"state"`
	ActivityMs  int64  `json:"activity_ms"`
	UpdatedAtMs int64  `json:"updated_at_ms"`
	DurationMs  *int64 `json:"duration_ms"`
}

type Artist struct {
	DbID       int64  `json:"db_id"`
	ArtistName string `json:"artist_name"`
}

type Album struct {
	DbID       int64  `json:"db_id"`
	AlbumTitle string `json:"album_title"`
	Year       int    `json:"year"`
}

type Track struct {
	DbID    int64    `json:"db_id"`
	Title   string   `json:"title"`
	Artists []Artist `json:"artists"`
	Albums  []Album  `json:"albums"`
}

var coverCache = map[int64]string{}

func uploadCover(albumID int64) (string, error) {
	if config.Images.Uploader == UploaderNone {
		return "", fmt.Errorf("image uploads disabled")
	}

	if url, ok := coverCache[albumID]; ok {
		return url, nil
	}

	resp, err := http.Get(fmt.Sprintf("%s/api/albums/%d/cover", config.BaseURL, albumID))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

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

	coverCache[albumID] = url
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
	resp, err := http.Get(config.BaseURL + "/api/playbacks?active=true")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("playbacks API returned status %d", resp.StatusCode)
	}

	var result []Playback
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return nil, nil
	}
	return &result[0], nil
}

func fetchTrack(id int64) (*Track, error) {
	resp, err := http.Get(fmt.Sprintf("%s/api/tracks/%d?inc=albums,artists", config.BaseURL, id))
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

func main() {
	if err := loadConfig("config.json"); err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("Error loading config: %v", err)
		}
	}

	if config.Images.Uploader == UploaderImgur && config.Images.ImgurClientID == "" {
		log.Fatal("imgur client_id is required when image_uploader is set to \"imgur\"")
	}

	err := client.Login("1474543583473176846")
	if err != nil {
		log.Fatal(err)
	}
	defer client.Logout()

	log.Println("Rich presence is running. Press Ctrl+C to exit.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	var lastTrackID int64
	var lastState string
	var lastPositionMs int64
	var cachedTrack *Track
	var cachedImage string

	ticker := time.NewTicker(time.Duration(config.PollIntervalSec) * time.Second)
	defer ticker.Stop()

	poll := func() {
		playback, err := fetchActivePlayback()
		if err != nil {
			log.Printf("Error fetching playback: %v", err)
			return
		}

		if playback == nil || (playback.State != "playing" && playback.State != "paused") {
			if lastState != "" {
				if err := client.ClearActivity(); err != nil {
					log.Printf("Error clearing activity: %v", err)
				} else {
					log.Println("No active playback, cleared presence.")
				}
			}
			lastTrackID = 0
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
			if len(track.Albums) > 0 {
				if url, err := uploadCover(track.Albums[0].DbID); err != nil {
					log.Printf("Error uploading cover: %v", err)
				} else {
					cachedImage = url
				}
			}

			artistNames := make([]string, len(track.Artists))
			for i, a := range track.Artists {
				artistNames[i] = a.ArtistName
			}
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

		artistNames := make([]string, len(cachedTrack.Artists))
		for i, a := range cachedTrack.Artists {
			artistNames[i] = a.ArtistName
		}

		activity := client.Activity{
			Type:       client.ActivityListening,
			Details:    cachedTrack.Title,
			LargeImage: cachedImage,
			LargeText:  strings.Join(artistNames, ", "),
		}

		if len(cachedTrack.Albums) > 0 {
			album := cachedTrack.Albums[0]
			if album.Year != 0 {
				activity.State = fmt.Sprintf("%s (%d)", album.AlbumTitle, album.Year)
			} else {
				activity.State = album.AlbumTitle
			}
		}

		if playback.State == "playing" {
			nowMs := time.Now().UnixMilli()
			effectiveMs := playback.PositionMs + (nowMs - playback.UpdatedAtMs)
			if playback.DurationMs != nil && effectiveMs > *playback.DurationMs {
				effectiveMs = *playback.DurationMs
			}
			start := time.Now().Add(-time.Duration(effectiveMs) * time.Millisecond)
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
