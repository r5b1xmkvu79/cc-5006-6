// Command genassets manufactures the archival test media set: landscape
// interview, narrow old tape, rotated phone video, silent video, stills
// (landscape, EXIF-rotated portrait, vertical manuscript, extreme narrow
// strip). Every asset carries colored edge/corner markers so automated
// checks can verify framing and orientation in rendered output.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"oralcut/internal/media"
)

// Corner markers (60px) on the stored frame:
//   top-left red, top-right green, bottom-right blue, bottom-left yellow
// Edge strips (12px): top magenta, bottom cyan.
// NB: in drawbox expressions w/h refer to the box itself; input
// dimensions are iw/ih.
func markers() string {
	return "drawbox=x=0:y=0:w=iw:h=12:color=magenta:t=fill," +
		"drawbox=x=0:y=ih-12:w=iw:h=12:color=cyan:t=fill," +
		"drawbox=x=0:y=0:w=60:h=60:color=red:t=fill," +
		"drawbox=x=iw-60:y=0:w=60:h=60:color=green:t=fill," +
		"drawbox=x=iw-60:y=ih-60:w=60:h=60:color=blue:t=fill," +
		"drawbox=x=0:y=ih-60:w=60:h=60:color=yellow:t=fill"
}

// manuscriptLines adds horizontal "text" lines for the vertical scroll.
func manuscriptLines() string {
	var b strings.Builder
	b.WriteString("color=0xf5f0e0:size=1200x2400")
	for y := 120; y < 2360; y += 90 {
		w := 700 + (y*37)%380 // ragged line lengths like handwriting
		fmt.Fprintf(&b, ",drawbox=x=100:y=%d:w=%d:h=14:color=0x2a2a2a:t=fill", y, w)
	}
	return b.String()
}

const longTitle = "口述历史访谈·城南老照相馆记忆——受访者：李秀兰（1938年生），" +
	"讲述一九五零年代至一九八零年代街区变迁、照相馆学徒制度与家庭相册的流转；" +
	"Oral History Interview, Session 07, Reel 12 —— 存档标题压力测试：超长元信息、" +
	"多语言混排与标点符号，必须完整保存不得截断。"

func main() {
	ffmpeg := flag.String("ffmpeg", os.Getenv("ORALCUT_FFMPEG"), "ffmpeg binary")
	out := flag.String("out", "assets", "output directory")
	flag.Parse()
	if *ffmpeg == "" {
		fmt.Fprintln(os.Stderr, "-ffmpeg or ORALCUT_FFMPEG required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}

	run := func(args ...string) {
		cmd := exec.Command(*ffmpeg, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fatal(fmt.Errorf("%v: %w", args, err))
		}
	}
	p := func(name string) string { return filepath.Join(*out, name) }

	// 1) Landscape interview 16:9 with tone and very long title metadata.
	run("-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-vf", markers(),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
		"-metadata", "title="+longTitle,
		"-y", p("interview_16x9.mp4"))

	// 2) Narrow old tape 4:3.
	run("-f", "lavfi", "-i", "testsrc2=size=640x480:rate=30:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=220:duration=6",
		"-vf", markers(),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
		"-metadata", "title=1994年家庭录像带",
		"-y", p("oldtape_4x3.mp4"))

	// 3) Phone video stored landscape, display-rotated 90 via metadata.
	// -display_rotation is an input option in ffmpeg 7: attach it to the
	// demux, then stream-copy so the display matrix lands in the output.
	tmp := p("phone_landscape_tmp.mp4")
	run("-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30:duration=5",
		"-f", "lavfi", "-i", "sine=frequency=330:duration=5",
		"-vf", markers(),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac",
		"-y", tmp)
	run("-display_rotation:v:0", "90", "-i", tmp, "-c", "copy",
		"-metadata", "title=竖屏手机访谈",
		"-y", p("phone_rotated.mp4"))
	os.Remove(tmp)

	// 4) Silent video (no audio track).
	run("-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30:duration=4",
		"-vf", markers(),
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-y", p("noaudio_16x9.mp4"))

	// 5) Landscape photo.
	run("-f", "lavfi", "-i", "testsrc2=size=3000x2000:rate=1:duration=1",
		"-vf", markers(), "-frames:v", "1", "-q:v", "2",
		"-y", p("photo_landscape.jpg"))

	// 6) Portrait photo stored landscape with EXIF orientation 6.
	tmpJpg := p("portrait_tmp.jpg")
	run("-f", "lavfi", "-i", "testsrc2=size=1200x800:rate=1:duration=1",
		"-vf", markers(), "-frames:v", "1", "-q:v", "2",
		"-y", tmpJpg)
	data, err := os.ReadFile(tmpJpg)
	if err != nil {
		fatal(err)
	}
	exifData, err := media.SetEXIFOrientation(data, 6)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(p("photo_portrait_exif6.jpg"), exifData, 0o644); err != nil {
		fatal(err)
	}
	os.Remove(tmpJpg)

	// 7) Vertical manuscript scan.
	run("-f", "lavfi", "-i", manuscriptLines(),
		"-vf", markers(), "-frames:v", "1", "-q:v", "2",
		"-y", p("manuscript_vertical.jpg"))

	// 8) Extreme narrow strip.
	run("-f", "lavfi", "-i", "testsrc2=size=4000x160:rate=1:duration=1",
		"-vf", markers(), "-frames:v", "1", "-q:v", "2",
		"-y", p("extreme_narrow.jpg"))

	fmt.Println("assets generated in", *out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "genassets:", err)
	os.Exit(1)
}
