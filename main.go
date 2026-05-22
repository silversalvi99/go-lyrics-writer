package main

import (
	"database/sql"
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
	_ "github.com/mattn/go-sqlite3"
)

const dbPath = "/root/data/lyrics.db"

type Processor struct {
	db *sql.DB
}

func main() {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	db.Exec("CREATE TABLE IF NOT EXISTS processed (path TEXT PRIMARY KEY, processed_at DATETIME)")
	proc := &Processor{db: db}

	log.Println("Starting initial recursive scan...")
	filepath.Walk("/music", func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".mp3") {
			proc.processTrack(path)
		}
		return nil
	})

	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()
	watcher.Add("/music")

	log.Println("Monitoring /music for changes...")
	for {
		select {
		case event := <-watcher.Events:
			if (event.Op&fsnotify.Create == fsnotify.Create) && strings.HasSuffix(strings.ToLower(event.Name), ".mp3") {
				time.Sleep(2 * time.Second)
				proc.processTrack(event.Name)
			}
		}
	}
}

// getMetadataFromPath extracts artist/title from directory structure (Highest Reliability)
func getMetadataFromPath(path string) (string, string) {
	parts := strings.Split(path, string(os.PathSeparator))
	// Expected: /music/Artist/Album/Song.mp3 or /music/Song.mp3
	if len(parts) >= 4 {
		return parts[len(parts)-3], strings.TrimSuffix(parts[len(parts)-1], ".mp3")
	}
	if len(parts) >= 2 {
		return "", strings.TrimSuffix(parts[len(parts)-1], ".mp3")
	}
	return "", ""
}

// sanitizeMetadata cleans strings using regex
func sanitizeMetadata(artist, title string) (string, string) {
	re := regexp.MustCompile(`\(.*?\)|\[.*?\]`)
	title = re.ReplaceAllString(title, "")
	reFeat := regexp.MustCompile(`(?i)(feat\.|ft\.|featuring)`)
	title = reFeat.ReplaceAllString(title, "")
	return strings.TrimSpace(artist), strings.TrimSpace(title)
}

func (p *Processor) processTrack(path string) {
	var exists string
	if err := p.db.QueryRow("SELECT path FROM processed WHERE path = ?", path).Scan(&exists); err == nil {
		return
	}

	// 1. Try Path-based extraction
	artist, title := getMetadataFromPath(path)

	// 2. Fallback to ID3 tags if path is insufficient
	if artist == "" || title == "" {
		f, err := os.Open(path)
		if err == nil {
			m, _ := tag.ReadFrom(f)
			a, t := sanitizeMetadata(m.Artist(), m.Title())
			if artist == "" { artist = a }
			if title == "" { title = t }
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
			tagFile.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
				Encoding: id3v2.EncodingUTF8, Language: "ita", ContentDescriptor: "Lyrics", Lyrics: lyrics,
			})
			tagFile.Save()
			tagFile.Close()
			p.db.Exec("INSERT INTO processed (path, processed_at) VALUES (?, ?)", path, time.Now())
			log.Printf("Successfully injected: %s", title)
		}
	}
}

func fetchLyrics(artist, title string) (string, error) {
	u := fmt.Sprintf("https://lrclib.net/api/get?artist_name=%s&track_name=%s", url.QueryEscape(artist), url.QueryEscape(title))
	resp, err := http.Get(u)
	if err != nil || resp.StatusCode != http.StatusOK { return "", fmt.Errorf("API error") }
	defer resp.Body.Close()

	var result struct {
		PlainLyrics string `json:"plainLyrics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.PlainLyrics == "" {
		return "", fmt.Errorf("not found")
	}
	return result.PlainLyrics, nil
}