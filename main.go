package main

import (
	"database/sql"
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

const dbPath = "./data/lyrics.db"

type Processor struct {
	db *sql.DB
}

func main() {
	// 1. Setup Database
	db, _ := sql.Open("sqlite3", dbPath)
	db.Exec("CREATE TABLE IF NOT EXISTS processed (path TEXT PRIMARY KEY, processed_at DATETIME)")
	proc := &Processor{db: db}

	// 2. Initial Scan
	log.Println("Performing initial recursive scan...")
	filepath.Walk("/music", func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() && strings.HasSuffix(path, ".mp3") {
			proc.processTrack(path)
		}
		return nil
	})

	// 3. Watcher for new files
	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()
	watcher.Add("/music")

	log.Println("Monitoring for new changes...")
	for {
		select {
		case event := <-watcher.Events:
			if (event.Op&fsnotify.Create == fsnotify.Create) && strings.HasSuffix(event.Name, ".mp3") {
				time.Sleep(1 * time.Second) // Wait for Deemix to finish writing
				proc.processTrack(event.Name)
			}
		}
	}
}

func (p *Processor) processTrack(path string) {
	// Check if already processed
	var exists string
	err := p.db.QueryRow("SELECT path FROM processed WHERE path = ?", path).Scan(&exists)
	if err == nil {
		return // Already done
	}

	// Extract tags
	f, _ := os.Open(path)
	defer f.Close()
	m, _ := tag.ReadFrom(f)

	log.Printf("Injecting lyrics for: %s - %s", m.Artist(), m.Title())

	// Fetch & Inject
	lyrics, err := fetchLyrics(m.Artist(), m.Title())
	if err == nil {
		tagFile, _ := id3v2.Open(path, id3v2.Options{Parse: true})
		tagFile.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
			Encoding: id3v2.EncodingUTF8, Language: "ita", ContentDescriptor: "Lyrics", Lyrics: lyrics,
		})
		tagFile.Save()
		p.db.Exec("INSERT INTO processed (path, processed_at) VALUES (?, ?)", path, time.Now())
		log.Println("Done.")
	}
}

func fetchLyrics(artist, title string) (string, error) {
	apiURL := fmt.Sprintf("https://lrclib.net/api/get?artist_name=%s&track_name=%s", url.QueryEscape(artist), url.QueryEscape(title))
	resp, err := http.Get(apiURL)
	if err != nil || resp.StatusCode != 200 { return "", fmt.Errorf("not found") }
	defer resp.Body.Close()
	// Simplified JSON parsing for example
	return "Lyrics fetched from LRCLIB...", nil 
}