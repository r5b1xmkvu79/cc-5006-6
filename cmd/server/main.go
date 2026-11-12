// Command server runs the archive editing station: HTTP API plus the
// built Vue frontend when available.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"oralcut/internal/api"
)

func main() {
	ffmpeg := flag.String("ffmpeg", os.Getenv("ORALCUT_FFMPEG"), "ffmpeg binary")
	ffprobe := flag.String("ffprobe", os.Getenv("ORALCUT_FFPROBE"), "ffprobe binary")
	addr := flag.String("addr", ":8080", "listen address")
	data := flag.String("data", "data", "data root (projects, deliver, tmp)")
	mediaRoot := flag.String("media-root", "assets", "root for importable media")
	static := flag.String("static", "web/dist", "built frontend directory")
	flag.Parse()
	if *ffmpeg == "" || *ffprobe == "" {
		log.Fatal("-ffmpeg/-ffprobe or ORALCUT_FFMPEG/ORALCUT_FFPROBE required")
	}
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			log.Fatal(err)
		}
		return a
	}
	srv := api.New(abs(*ffprobe), abs(*ffmpeg),
		filepath.Join(abs(*data), "projects"),
		filepath.Join(abs(*data), "deliver"),
		abs(*mediaRoot),
		filepath.Join(abs(*data), "tmp"))

	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Routes())
	if st, err := os.Stat(abs(*static)); err == nil && st.IsDir() {
		mux.Handle("/", http.FileServer(http.Dir(abs(*static))))
	}
	log.Printf("oralcut server on %s (media root %s)", *addr, abs(*mediaRoot))
	log.Fatal(http.ListenAndServe(*addr, mux))
}
