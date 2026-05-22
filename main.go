package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
	"github.com/fsnotify/fsnotify"
	"go.etcd.io/bbolt"
)

const (
	dbPath     = "/root/data/lyrics.db"
	bucketName = "processed_files"
)

func main() {
	// 1. Initialize BoltDB (Pure Go, high performance, no CGO)
	db, err := bbolt.Open(dbPath, 0600, nil)
	if err != nil {
		log.Fatal("Failed to open BoltDB:", err)
	}
	defer db.Close()

	db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		return err
	})

	// 2. Perform initial recursive scan of the music directory
	log.Println("Starting initial recursive scan...")
	filepath.Walk("/music", func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".mp3") {
			processTrack(db, path)
		}
		return nil
	})

	// 3. Setup file system watcher for real-time updates
	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()
	watcher.Add("/music")

	log.Println("Monitoring /music for changes...")

	// Idiomatic range loop over the events channel
	for event := range watcher.Events {
		if (event.Op&fsnotify.Create == fsnotify.Create) && strings.HasSuffix(strings.ToLower(event.Name), ".mp3") {
			// Small delay to ensure the file system has finished writing the file
			time.Sleep(2 * time.Second)
			processTrack(db, event.Name)
		}
	}
}

// getMetadataFromPath extracts artist/title from directory structure
func getMetadataFromPath(path string) (string, string) {
	parts := strings.Split(path, string(os.PathSeparator))
	if len(parts) >= 4 {
		return parts[len(parts)-3], strings.TrimSuffix(parts[len(parts)-1], ".mp3")
	}
	return "", strings.TrimSuffix(parts[len(parts)-1], ".mp3")
}

// sanitizeMetadata cleans strings using regex
func sanitizeMetadata(artist, title string) (string, string) {
	re := regexp.MustCompile(`\(.*?\)|\[.*?\]`)
	title = re.ReplaceAllString(title, "")
	reFeat := regexp.MustCompile(`(?i)(feat\.|ft\.|featuring)`)
	title = reFeat.ReplaceAllString(title, "")
	return strings.TrimSpace(artist), strings.TrimSpace(title)
}

// processTrack handles the workflow: Check DB -> Extract Metadata -> Fetch Lyrics -> Inject
func processTrack(db *bbolt.DB, path string) {
	// Check if already processed
	exists := false
	db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b.Get([]byte(path)) != nil {
			exists = true
		}
		return nil
	})
	if exists {
		return
	}

	// 1. Path-based extraction (Highest Reliability)
	artist, title := getMetadataFromPath(path)

	// 2. Fallback to ID3 tags if path is insufficient
	if artist == "" || title == "" {
		f, err := os.Open(path)
		if err == nil {
			m, _ := tag.ReadFrom(f)
			a, t := sanitizeMetadata(m.Artist(), m.Title())
			if artist == "" {
				artist = a
			}
			if title == "" {
				title = t
			}
			f.Close()
		}
	}

	log.Printf("Searching for: %s - %s", artist, title)

	// 3. Search with fallback (Artist+Title -> Title-only)
	lyrics, err := fetchLyrics(artist, title)
	if err != nil {
		lyrics, err = fetchLyrics("", title) // Title-only fallback
	}

	if err == nil {
		tagFile, err := id3v2.Open(path, id3v2.Options{Parse: true})
		if err == nil {
			defer tagFile.Close()
			tagFile.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
				Encoding:          id3v2.EncodingUTF8,
				Language:          "ita",
				ContentDescriptor: "Lyrics",
				Lyrics:            lyrics,
			})
			tagFile.Save()
			db.Update(func(tx *bbolt.Tx) error {
				return tx.Bucket([]byte(bucketName)).Put([]byte(path), []byte("1"))
			})
			log.Printf("Successfully injected: %s", title)
		}
	}
}

// fetchLyrics communicates with LRCLIB API to retrieve lyrics
func fetchLyrics(artist, title string) (string, error) {
	u := fmt.Sprintf("https://lrclib.net/api/get?artist_name=%s&track_name=%s",
		url.QueryEscape(artist), url.QueryEscape(title))

	resp, err := http.Get(u)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error")
	}
	defer resp.Body.Close()

	var res struct {
		PlainLyrics string `json:"plainLyrics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || res.PlainLyrics == "" {
		return "", fmt.Errorf("not found")
	}
	return res.PlainLyrics, nil
}