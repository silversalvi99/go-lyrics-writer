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
	"strings"
	"time"

	"github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
	"github.com/fsnotify/fsnotify"
	_ "github.com/mattn/go-sqlite3"
)

// dbPath points to the volume-mounted location for persistence
const dbPath = "/root/data/lyrics.db"

type Processor struct {
	db *sql.DB
}

func main() {
	// 1. Initialize SQLite database for persistence tracking
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()
	
	// Ensure the tracking table exists
	db.Exec("CREATE TABLE IF NOT EXISTS processed (path TEXT PRIMARY KEY, processed_at DATETIME)")
	proc := &Processor{db: db}

	// 2. Perform an initial recursive scan of the music directory
	log.Println("Starting initial recursive scan...")
	filepath.Walk("/music", func(path string, info os.FileInfo, err error) error {
		// Only target MP3 files
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".mp3") {
			proc.processTrack(path)
		}
		return nil
	})

	// 3. Setup file system watcher for real-time updates
	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()
	watcher.Add("/music")

	log.Println("Monitoring /music for new changes...")
	for {
		select {
		case event := <-watcher.Events:
			// Process only new file creation events for MP3s
			if (event.Op&fsnotify.Create == fsnotify.Create) && strings.HasSuffix(strings.ToLower(event.Name), ".mp3") {
				// Short delay to ensure the file system has finished writing the file
				time.Sleep(2 * time.Second) 
				proc.processTrack(event.Name)
			}
		case err := <-watcher.Errors:
			log.Printf("Watcher error: %v", err)
		}
	}
}

// processTrack handles the workflow: Check DB -> Fetch Lyrics -> Inject into Tag
func (p *Processor) processTrack(path string) {
	// Skip if the file path is already present in our database
	var exists string
	err := p.db.QueryRow("SELECT path FROM processed WHERE path = ?", path).Scan(&exists)
	if err == nil {
		return 
	}

	// Extract metadata from the file
	f, err := os.Open(path)
	if err != nil { return }
	defer f.Close()
	
	m, err := tag.ReadFrom(f)
	if err != nil { return }

	log.Printf("Processing: %s - %s", m.Artist(), m.Title())

	// Attempt to fetch lyrics from LRCLIB
	lyrics, err := fetchLyrics(m.Artist(), m.Title())
	if err != nil {
		log.Printf("Lyrics not found for: %s", m.Title())
		return
	}

	// Open the file for ID3 tag manipulation
	tagFile, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err == nil {
		// Inject lyrics using the USLT (Unsynchronized Lyrics) frame
		tagFile.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
			Encoding: id3v2.EncodingUTF8, Language: "ita", ContentDescriptor: "Lyrics", Lyrics: lyrics,
		})
		tagFile.Save()
		tagFile.Close()
		
		// Mark file as processed in SQLite
		p.db.Exec("INSERT INTO processed (path, processed_at) VALUES (?, ?)", path, time.Now())
		log.Printf("Successfully injected lyrics for: %s", m.Title())
	}
}

// fetchLyrics communicates with LRCLIB API to retrieve lyrics
func fetchLyrics(artist, title string) (string, error) {
	apiURL := fmt.Sprintf("https://lrclib.net/api/get?artist_name=%s&track_name=%s", 
		url.QueryEscape(artist), url.QueryEscape(title))
	
	resp, err := http.Get(apiURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error")
	}
	defer resp.Body.Close()

	// Parse JSON response specifically for the plainLyrics field
	var result struct {
		PlainLyrics string `json:"plainLyrics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.PlainLyrics == "" {
		return "", fmt.Errorf("no lyrics found")
	}
	return result.PlainLyrics, nil
}