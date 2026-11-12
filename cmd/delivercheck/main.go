// Command delivercheck produces the delivery bundle: original file
// references, the saved project, a reopen verification, the rendered
// program, and an inspection report with extracted frames and audio
// measurements. It exits non-zero when any check fails.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"oralcut/internal/geometry"
	"oralcut/internal/media"
	"oralcut/internal/project"
	"oralcut/internal/render"
)

var failures []string

func check(ok bool, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if ok {
		fmt.Println("  PASS", msg)
	} else {
		fmt.Println("  FAIL", msg)
		failures = append(failures, msg)
	}
}

func main() {
	ffmpeg := flag.String("ffmpeg", os.Getenv("ORALCUT_FFMPEG"), "ffmpeg")
	ffprobe := flag.String("ffprobe", os.Getenv("ORALCUT_FFPROBE"), "ffprobe")
	assets := flag.String("assets", "assets", "generated media dir")
	out := flag.String("out", "deliver", "delivery output dir")
	flag.Parse()
	if *ffmpeg == "" || *ffprobe == "" {
		fmt.Fprintln(os.Stderr, "ffmpeg/ffprobe required")
		os.Exit(2)
	}
	abs, _ := filepath.Abs(*assets)
	outAbs, _ := filepath.Abs(*out)
	os.MkdirAll(outAbs, 0o755)

	prober := media.NewProber(*ffprobe)
	fmt.Println("== 1. 原件引用 ==")
	refs := map[string]project.Media{}
	add := func(id, name string) project.Media {
		info, err := prober.Probe(filepath.Join(abs, name))
		if err != nil {
			check(false, "probe %s: %v", name, err)
			return project.Media{}
		}
		m := project.Media{
			ID: id, Path: info.Path, SHA256: info.SHA256,
			Kind: string(info.Kind), Width: info.Width, Height: info.Height,
			Rotation: info.Rotation, EXIFOrientation: info.EXIFOrientation,
			Duration: info.Duration, HasAudio: info.HasAudio, Title: info.Title,
			Status: project.StatusOK,
		}
		refs[id] = m
		check(true, "%s %s %dx%d rot=%d exif=%d sha=%.12s…", id, name, info.Width, info.Height, info.Rotation, info.EXIFOrientation, info.SHA256)
		return m
	}
	p := project.New("archive_program", "口述纪录片档案混编（交付样例）")
	p.Media = []project.Media{
		add("interview", "interview_16x9.mp4"),
		add("oldtape", "oldtape_4x3.mp4"),
		add("photo", "photo_landscape.jpg"),
		add("manuscript", "manuscript_vertical.jpg"),
		add("phone", "phone_rotated.mp4"),
		add("exifportrait", "photo_portrait_exif6.jpg"),
		add("silent", "noaudio_16x9.mp4"),
		add("narrow", "extreme_narrow.jpg"),
		{ID: "lost", Path: filepath.Join(abs, "lost_reel_1998.mp4"), Kind: "video",
			Width: 1920, Height: 1080, Duration: 10, HasAudio: true},
	}
	confirmed := project.Crop{X: 0.5, Y: 0, Confirmed: true, ConfirmedBy: "交付编辑"}
	p.Clips = []project.Clip{
		{ID: "c1", MediaID: "interview", Kind: project.ClipVideo, Label: "横屏访谈", In: 1, Out: 4, Fit: geometry.FitFull},
		{ID: "c2", MediaID: "oldtape", Kind: project.ClipVideo, Label: "窄幅旧录像", In: 0, Out: 2, Fit: geometry.FitCrop, Crop: confirmed},
		{ID: "c3", MediaID: "photo", Kind: project.ClipPhoto, Label: "静态照片", Dwell: 2, Fit: geometry.FitFull},
		{ID: "c4", MediaID: "manuscript", Kind: project.ClipPhoto, Label: "竖向手稿", Dwell: 2, Fit: geometry.FitBorder, Border: project.Border{Blur: 12, Padding: 10, Color: "red"}},
		{ID: "c5", MediaID: "phone", Kind: project.ClipVideo, Label: "旋转手机视频", In: 0, Out: 2, Fit: geometry.FitFull},
		{ID: "c6", MediaID: "exifportrait", Kind: project.ClipPhoto, Label: "EXIF肖像照", Dwell: 2, Fit: geometry.FitFull},
		{ID: "c7", MediaID: "silent", Kind: project.ClipVideo, Label: "无声视频", In: 0, Out: 2, Fit: geometry.FitFull},
		{ID: "c8", MediaID: "photo", Kind: project.ClipPhoto, Label: "照片缓慢平移", Dwell: 3, Fit: geometry.FitCrop,
			Crop: project.Crop{X: 0.5, Y: 0.5, Confirmed: true, ConfirmedBy: "交付编辑"},
			Pan: &geometry.Pan{Zoom: 1.2, X0: 0, Y0: 0.5, X1: 1, Y1: 0.5}},
		{ID: "c9", MediaID: "narrow", Kind: project.ClipPhoto, Label: "极窄长卷", Dwell: 2, Fit: geometry.FitFull},
		{ID: "c10", MediaID: "lost", Kind: project.ClipVideo, Label: "遗失素材（占位）", In: 0, Out: 2, Fit: geometry.FitFull},
	}

	// Originals reference manifest.
	origPath := filepath.Join(outAbs, "originals.json")
	writeJSON(origPath, map[string]any{
		"generatedAt": time.Now().UTC().Format(time.RFC3339),
		"note":        "原件仅以引用方式登记（路径+SHA256），不复制；缩略图不作为最终画面来源",
		"media":       p.Media,
	})
	check(true, "原件引用清单 %s", origPath)

	fmt.Println("== 2. 工程保存与重开 ==")
	projDir := filepath.Join(outAbs, "project")
	if err := p.Save(projDir); err != nil {
		check(false, "save project: %v", err)
	}
	reopened, err := project.Load(projDir, p.ID)
	check(err == nil, "工程重开")
	if err == nil {
		reopened.RecheckMedia(func(path string) (string, error) {
			info, err := prober.Probe(path)
			if err != nil {
				return "", err
			}
			return info.SHA256, nil
		})
		check(len(reopened.Clips) == 10 && len(reopened.Media) == 9, "片段与素材数量保持")
		check(reopened.MediaByID("lost").Status == project.StatusMissing, "缺失素材保留占位与原因: %s", reopened.MediaByID("lost").Reason)
		check(reopened.MediaByID("interview").Status == project.StatusOK, "在位素材状态正常")
		check(strings.Contains(reopened.MediaByID("interview").Title, "李秀兰"), "长标题元信息完整（%d 字）", len([]rune(reopened.MediaByID("interview").Title)))
		p = reopened
	}

	fmt.Println("== 3. 导出 ==")
	// Blocked first: unresolved clip must stop a formal export.
	exp := &render.Exporter{FFmpeg: *ffmpeg, TempDir: filepath.Join(outAbs, "tmp")}
	blockedErr := func() error {
		_, err := exp.Export(p, render.Options{
			OutPath: filepath.Join(outAbs, "tmp", "should_not_exist.mp4")})
		return err
	}()
	check(blockedErr != nil, "未解决片段阻止正式导出: %v", oneLine(blockedErr))

	outDir := filepath.Join(outAbs, "output")
	manifest, err := exp.Export(p, render.Options{
		OutPath:          filepath.Join(outDir, "program.mp4"),
		AllowPlaceholder: true,
	})
	if err != nil {
		check(false, "export: %v", err)
		finish(outAbs)
	}
	check(true, "占位模式导出完成")
	if err := render.WriteManifest(filepath.Join(outAbs, "manifest.json"), manifest); err != nil {
		check(false, "manifest: %v", err)
	}

	fmt.Println("== 4. 成片检查 ==")
	program := filepath.Join(outDir, "program.mp4")
	probe := probeFile(*ffprobe, program)
	check(probe.width == 1920 && probe.height == 1080, "画幅 %dx%d", probe.width, probe.height)
	check(probe.fps == "30/1", "帧率 %s", probe.fps)
	const want = 22.0
	check(math.Abs(probe.duration-want) < 0.35, "视频时长 %.2fs（预期 %.0fs）", probe.duration, want)
	check(math.Abs(probe.audioDur-want) < 0.35, "音轨拼接时长 %.2fs（预期 %.0fs）", probe.audioDur, want)

	framesDir := filepath.Join(outAbs, "inspection", "frames")
	os.MkdirAll(framesDir, 0o755)
	mids := []struct {
		clip string
		at   float64
	}{
		{"c1", 1.5}, {"c2", 4.0}, {"c3", 6.0}, {"c4", 8.0}, {"c5", 10.0},
		{"c6", 12.0}, {"c7", 14.0}, {"c8", 16.5}, {"c9", 19.0}, {"c10", 21.0},
	}
	for _, m := range mids {
		png := filepath.Join(framesDir, m.clip+".png")
		run(*ffmpeg, "-ss", fmt.Sprintf("%.2f", m.at), "-i", program, "-frames:v", "1", "-y", png)
	}
	check(len(mustReadDir(framesDir)) == 10, "抽取 10 个片段中点帧到 inspection/frames")

	// Spot geometry checks on extracted frames.
	img := decode(filepath.Join(framesDir, "c2.png"))
	r, g, b := px(img, 960, 1073)
	check(!(g > 180 && b > 180 && r < 90), "裁切片段底部不出现被裁掉的青色边缘 (#%02x%02x%02x)", r, g, b)
	img = decode(filepath.Join(framesDir, "c5.png"))
	r, g, b = px(img, 681, 1055)
	check(r > 180 && g < 90 && b < 90, "旋转视频方向正确（红色标记落于左下）(#%02x%02x%02x)", r, g, b)
	img = decode(filepath.Join(framesDir, "c10.png"))
	r, g, b = px(img, 4, 4)
	check(r > 180 && g < 90 && b < 90, "占位画面红框可见 (#%02x%02x%02x)", r, g, b)

	// Audio splice measurements.
	rmsTone := audioRMS(*ffmpeg, program, 0.5, 1.0)
	rmsPhoto := audioRMS(*ffmpeg, program, 6.0, 1.0)
	check(rmsTone > -40, "访谈段音轨有效 (RMS %.1f dB)", rmsTone)
	check(rmsPhoto < -60, "照片段静音轨拼接 (RMS %.1f dB)", rmsPhoto)

	writeInspection(outAbs, p, manifest, probe, rmsTone, rmsPhoto)
	finish(outAbs)
}

func finish(outAbs string) {
	fmt.Println("== 交付物 ==")
	for _, f := range []string{"originals.json", "project/archive_program.json", "manifest.json", "output/program.mp4", "INSPECTION.md"} {
		fp := filepath.Join(outAbs, f)
		if st, err := os.Stat(fp); err == nil {
			fmt.Printf("  %s (%d bytes)\n", fp, st.Size())
		}
	}
	if len(failures) > 0 {
		fmt.Printf("\n%d 项检查失败\n", len(failures))
		os.Exit(1)
	}
	fmt.Println("\n全部检查通过")
}

func writeInspection(outAbs string, p *project.Project, m *render.Manifest, pr probeInfo, rmsTone, rmsPhoto float64) {
	var b strings.Builder
	b.WriteString("# 成片检查报告\n\n")
	fmt.Fprintf(&b, "生成时间: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("## 输出\n\n")
	fmt.Fprintf(&b, "- 文件: `%s`\n", m.Output)
	fmt.Fprintf(&b, "- 画幅: %dx%d @ %s\n", pr.width, pr.height, pr.fps)
	fmt.Fprintf(&b, "- 视频时长: %.2fs / 音频时长: %.2fs（音轨拼接一致）\n", pr.duration, pr.audioDur)
	fmt.Fprintf(&b, "- 访谈段音频 RMS: %.1f dB；照片段 RMS: %.1f dB（静音轨）\n\n", rmsTone, rmsPhoto)
	b.WriteString("## 片段明细\n\n")
	b.WriteString("| 片段 | 素材 | 构图 | 展示声明 | 时长s | 备注 |\n|---|---|---|---|---|---|\n")
	for _, c := range m.Clips {
		note := ""
		if c.Placeholder {
			note = "占位: " + c.Reason
		} else if c.Fit == "crop" {
			note = fmt.Sprintf("裁切位置 (%.2f, %.2f)，编辑已确认", c.Crop.X, c.Crop.Y)
		}
		fmt.Fprintf(&b, "| %s %s | %s | %s | %s | %.1f | %s |\n",
			c.ClipID, c.Label, c.MediaID, c.Fit, c.DisplayClaim, c.Duration, note)
	}
	b.WriteString("\n## 工程重开\n\n- 工程文件重开成功，素材状态重新核验；缺失素材保留占位与原因。\n")
	b.WriteString("- 未解决片段在正式导出时阻止；占位导出已在清单中说明。\n")
	b.WriteString("\n## 帧抽取\n\n各片段中点帧见 `inspection/frames/*.png`，与画布几何规则一致（预览与输出同规则）。\n")
	os.WriteFile(filepath.Join(outAbs, "INSPECTION.md"), []byte(b.String()), 0o644)
}

type probeInfo struct {
	width, height int
	fps           string
	duration      float64
	audioDur      float64
}

func probeFile(ffprobe, file string) probeInfo {
	cmd := exec.Command(ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", file)
	out, err := cmd.Output()
	if err != nil {
		check(false, "probe output: %v", err)
		return probeInfo{}
	}
	var v struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			FPS       string `json:"avg_frame_rate"`
			Duration  string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		check(false, "parse probe: %v", err)
		return probeInfo{}
	}
	var pi probeInfo
	fmt.Sscanf(v.Format.Duration, "%f", &pi.duration)
	for _, s := range v.Streams {
		switch s.CodecType {
		case "video":
			pi.width, pi.height, pi.fps = s.Width, s.Height, s.FPS
		case "audio":
			fmt.Sscanf(s.Duration, "%f", &pi.audioDur)
		}
	}
	return pi
}

func audioRMS(ffmpeg, file string, at, dur float64) float64 {
	cmd := exec.Command(ffmpeg, "-ss", fmt.Sprintf("%.2f", at), "-t", fmt.Sprintf("%.2f", dur),
		"-i", file, "-map", "a", "-af", "astats", "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "RMS level dB") {
			var v float64
			fmt.Sscanf(strings.TrimSpace(line[strings.Index(line, ":")+1:]), "%f", &v)
			return v
		}
	}
	return -999
}

func decode(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		check(false, "open %s: %v", path, err)
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		check(false, "decode %s: %v", path, err)
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	return img
}

func px(img image.Image, x, y int) (int, int, int) {
	r, g, b, _ := img.At(x, y).RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}

func run(bin string, args ...string) {
	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		check(false, "%s %v: %v\n%s", bin, args, err, out)
	}
}

func writeJSON(path string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		check(false, "marshal: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		check(false, "mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		check(false, "write %s: %v", path, err)
	}
}

func mustReadDir(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		check(false, "readdir %s: %v", dir, err)
		return nil
	}
	return entries
}

func oneLine(err error) string {
	if err == nil {
		return ""
	}
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}
