package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/bogem/id3v2/v2"
	"github.com/fsnotify/fsnotify"
)

// Struct to parse the JSON response from LRCLIB
type LyricsResponse struct {
	PlainLyrics string `json:"plainLyrics"`
}

func main() {
	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()

	watcher.Add("/music")
	processedFiles := make(map[string]time.Time)

	log.Println("Lyrics Service started: monitoring /music...")

	for {
		select {
		case event := <-watcher.Events:
			if strings.HasSuffix(event.Name, ".part") || strings.HasSuffix(event.Name, ".tmp") {
				continue
			}

			if (event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write) &&
				(strings.HasSuffix(event.Name, ".mp3")) {

				if lastTime, ok := processedFiles[event.Name]; ok && time.Since(lastTime) < 5*time.Second {
					continue
				}

				time.Sleep(1 * time.Second)
				processedFiles[event.Name] = time.Now()

				log.Printf("Processing: %s", filepath.Base(event.Name))
				processTrack(event.Name)
			}
		case err := <-watcher.Errors:
			log.Printf("Error: %v", err)
		}
	}
}

func processTrack(path string) {
	// 1. Read metadata
	f, _ := id3v2.Open(path, id3v2.Options{Parse: true})
	defer f.Close()

	// 2. Fetch lyrics from LRCLIB
	lyrics, err := fetchLyrics(f.Artist(), f.Title())
	if err != nil {
		log.Printf("Lyrics not found for %s: %v", f.Title(), err)
		return
	}

	// 3. Inject lyrics
	f.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
		Encoding:          id3v2.EncodingUTF8,
		Language:          "ita",
		ContentDescriptor: "Lyrics",
		Lyrics:            lyrics,
	})
	f.Save()
	log.Printf("Successfully injected lyrics for: %s", f.Title())
}

func fetchLyrics(artist, title string) (string, error) {
	apiURL := fmt.Sprintf("https://lrclib.net/api/get?artist_name=%s&track_name=%s", 
		url.QueryEscape(artist), url.QueryEscape(title))
	
	resp, err := http.Get(apiURL)
	if err != nil || resp.StatusCode != 200 {
		return "", fmt.Errorf("api error")
	}
	defer resp.Body.Close()

	var data LyricsResponse
	json.NewDecoder(resp.Body).Decode(&data)
	return data.PlainLyrics, nil
}