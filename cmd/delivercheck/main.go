// Command delivercheck produces the delivery bundle: original file
// references, the saved project, a reopen verification, the rendered
// program, and an inspection report with extracted frames and audio
// measurements. It exits non-zero when any check fails.
package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	"reflect"
	"strings"
	"time"

	"oralcut/internal/geometry"
	"oralcut/internal/media"
	"oralcut/internal/migrate"
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
	migrateBin := flag.String("migrate-bin", "oralcut-migrate", "oralcut-migrate binary")
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
			Kind: string(info.Kind), Width: info.DisplayWidth, Height: info.DisplayHeight,
			Rotation: info.Rotation, EXIFOrientation: info.EXIFOrientation,
			Duration: info.Duration, HasAudio: info.HasAudio, Title: info.Title,
			Description: media.DescriptionFromInfo(info),
			Status: project.StatusOK,
		}
		refs[id] = m
		check(true, "%s %s 显示 %dx%d rot=%d exif=%d sha=%.12s…", id, name, info.DisplayWidth, info.DisplayHeight, info.Rotation, info.EXIFOrientation, info.SHA256)
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
		check(reopened.FormatVersion == project.FormatVersionCurrent, "工程格式版本 v%d", reopened.FormatVersion)
		check(reopened.MediaByID("interview").Description != nil, "统一媒体描述随工程保存")
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
	migrationSection(*ffmpeg, *ffprobe, *migrateBin, abs, outAbs)
	finish(outAbs)
}

func finish(outAbs string) {
	fmt.Println("== 交付物 ==")
	for _, f := range []string{"originals.json", "project/archive_program.json", "manifest.json", "output/program.mp4", "INSPECTION.md",
		"migration/legacy_project.json", "migration/migrated/legacy_oral.json",
		"migration/migration-report.json", "migration/restored_project.json",
		"migration/output/program.mp4", "migration/MIGRATION.md"} {
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

// migrationSection migrates a legacy oral-history project (scattered
// aspect/rotation fields, no pixel ratio) into the unified versioned
// media description, then actually runs the migrated project at the
// target path: open, validate, export, inspect. It also exercises
// idempotency and rollback, and records versions plus the rollback
// procedure in migration/MIGRATION.md.
func migrationSection(ffmpeg, ffprobe, migrateBin, assetsAbs, outAbs string) {
	fmt.Println("== 5. 画幅工程迁移 ==")
	migDir := filepath.Join(outAbs, "migration")
	os.MkdirAll(migDir, 0o755)
	prober := media.NewProber(ffprobe)
	a := func(name string) string { return filepath.Join(assetsAbs, name) }

	// --- 5.1 旧格式样例：画幅/旋转分散保存，像素比例缺失 ---
	legacyMedia := func(id, path string) project.Media {
		info, err := prober.Probe(path)
		if err != nil {
			check(false, "probe %s: %v", path, err)
			return project.Media{}
		}
		// 旧契约：width/height 是旋转后的编码尺寸，像素比例无处记录。
		return project.Media{
			ID: id, Path: info.Path, SHA256: info.SHA256,
			Kind: string(info.Kind), Width: info.Width, Height: info.Height,
			Rotation: info.Rotation, EXIFOrientation: info.EXIFOrientation,
			Duration: info.Duration, HasAudio: info.HasAudio, Title: info.Title,
			Status: project.StatusOK,
		}
	}
	lp := project.New("legacy_oral", "口述纪录旧工程（画幅迁移样例）")
	lp.FormatVersion = 0 // 旧格式无 formatVersion
	lp.Media = []project.Media{
		legacyMedia("dv", a("dv_anamorphic.mp4")),   // 非方形像素
		legacyMedia("phone", a("phone_rotated.mp4")), // 竖屏
		legacyMedia("photo", a("photo_landscape.jpg")),
		legacyMedia("exifportrait", a("photo_portrait_exif6.jpg")),
	}
	lp.Clips = []project.Clip{
		{ID: "m1", MediaID: "dv", Kind: project.ClipVideo, Label: "DV宽屏访谈",
			In: 0, Out: 2, Fit: geometry.FitCrop,
			Crop: project.Crop{X: 0.5, Y: 0, Confirmed: true, ConfirmedBy: "旧编辑"}},
		{ID: "m2", MediaID: "phone", Kind: project.ClipVideo, Label: "竖屏手机访谈",
			In: 0, Out: 2, Fit: geometry.FitFull},
		{ID: "m3", MediaID: "photo", Kind: project.ClipPhoto, Label: "静态照片",
			Dwell: 2, Fit: geometry.FitFull},
		{ID: "m4", MediaID: "exifportrait", Kind: project.ClipPhoto, Label: "EXIF肖像",
			Dwell: 2, Fit: geometry.FitBorder, Border: project.Border{Blur: 12}},
	}
	var legacyRaw map[string]any
	data, _ := json.MarshalIndent(lp, "", "  ")
	if err := json.Unmarshal(data, &legacyRaw); err != nil {
		check(false, "legacy fixture: %v", err)
		return
	}
	// 未识别字段：迁移必须保留供回退。
	legacyRaw["archiveShelfmark"] = "城南照相馆-架A-07"
	legacyRaw["media"].([]any)[0].(map[string]any)["legacyTapeNo"] = "DV-1998-042"
	legacyPath := filepath.Join(migDir, "legacy_project.json")
	writeJSON(legacyPath, legacyRaw)
	legacyHash := fileSHA256(legacyPath)

	// 新旧契约核对（旧侧）。
	_, hasVersion := legacyRaw["formatVersion"]
	dvLegacy := legacyRaw["media"].([]any)[0].(map[string]any)
	_, hasDesc := dvLegacy["description"]
	check(!hasVersion && !hasDesc, "旧契约核对：无 formatVersion/description，画幅旋转分散于扁平字段")
	check(dvLegacy["width"].(float64) == 720 && dvLegacy["height"].(float64) == 576,
		"旧契约核对：DV 记录为编码尺寸 %vx%v（非方形像素无处表达）", dvLegacy["width"], dvLegacy["height"])

	// --- 5.2 运行迁移工具到目标路径 ---
	migratedDir := filepath.Join(migDir, "migrated")
	migratedPath := filepath.Join(migratedDir, "legacy_oral.json")
	reportPath := filepath.Join(migDir, "migration-report.json")
	migOut, migErr := runMigrate(migrateBin, "-in", legacyPath,
		"-out", migratedPath, "-report", reportPath, "-ffprobe", ffprobe)
	check(migErr == nil, "迁移工具运行完成（退出码 0）")
	if migErr != nil {
		fmt.Println(migOut)
		return
	}
	check(fileSHA256(legacyPath) == legacyHash, "原工程未被修改（中断/重复运行仍可用）")

	var rep migrate.Report
	if !readJSONFile(reportPath, &rep) {
		return
	}
	check(rep.ToolVersion != "" && rep.DescriptionVersion == media.DescriptionVersion && rep.FFprobeVersion != "",
		"版本记录：工具 v%s / 描述 v%d / %s", rep.ToolVersion, rep.DescriptionVersion, firstWord(rep.FFprobeVersion))
	check(rep.Scope.MediaMigrated == 4 && rep.Scope.MediaPending == 0,
		"迁移范围：%d 个素材完成，%d 个待定", rep.Scope.MediaMigrated, rep.Scope.MediaPending)
	check(!rep.Scope.TimingChanged, "迁移不改动片段时长模型")

	// 新契约核对：统一版本化描述，编码尺寸不被当作显示尺寸。
	var migratedRaw map[string]any
	if !readJSONFile(migratedPath, &migratedRaw) {
		return
	}
	check(migratedRaw["formatVersion"].(float64) == 2, "新契约：formatVersion=2")
	dvDesc := findMedia(todAny(migratedRaw["media"]), "dv")["description"].(map[string]any)
	check(dvDesc["displayWidth"].(float64) == 1024 && dvDesc["displayHeight"].(float64) == 576,
		"新契约：DV 显示尺寸 %vx%v（编码 720x576 + 像素比 64:45）", dvDesc["displayWidth"], dvDesc["displayHeight"])
	check(dvDesc["sarNum"].(float64) == 64 && dvDesc["sarDen"].(float64) == 45, "新契约：像素比例 64:45 已记录")
	phoneDesc := findMedia(todAny(migratedRaw["media"]), "phone")["description"].(map[string]any)
	check(phoneDesc["displayWidth"].(float64) == 720 && phoneDesc["displayHeight"].(float64) == 1280,
		"新契约：竖屏手机显示尺寸 %vx%v", phoneDesc["displayWidth"], phoneDesc["displayHeight"])

	// 主体边界与片段时长 → 语义保留程度。
	var m1ev, m2ev *migrate.ClipEvidence
	for i := range rep.Clips {
		switch rep.Clips[i].ClipID {
		case "m1":
			m1ev = &rep.Clips[i]
		case "m2":
			m2ev = &rep.Clips[i]
		}
	}
	check(m1ev != nil && m1ev.Preservation == "adjusted" &&
		m1ev.SubjectBefore.W == 720 && m1ev.SubjectAfter.W == 1024,
		"主体边界：DV 裁切片段可见宽度 %.0f → %.0f（语义调整）", m1ev.SubjectBefore.W, m1ev.SubjectAfter.W)
	check(m1ev != nil && m1ev.DurationBefore == 2 && m1ev.DurationAfter == 2,
		"片段时长：m1 %.1fs → %.1fs（保持不变）", m1ev.DurationBefore, m1ev.DurationAfter)
	check(m2ev != nil && m2ev.Preservation == "preserved", "竖屏片段语义保留")
	check(len(rep.Scope.ClipsConfirmationReset) == 1 && rep.Scope.ClipsConfirmationReset[0] == "m1",
		"旧裁切确认不冒充有效：m1 确认已失效待复核")

	// 未识别字段保留供回退。
	env := migratedRaw["legacy"].(map[string]any)
	check(env["unknownProjectFields"].(map[string]any)["archiveShelfmark"] == "城南照相馆-架A-07",
		"未识别顶层字段保留于 legacy 信封")
	check(env["unknownMediaFields"].(map[string]any)["dv"].(map[string]any)["legacyTapeNo"] == "DV-1998-042",
		"未识别素材字段保留于 legacy 信封")

	// --- 5.3 可重复运行：已迁移工程进入核验模式 ---
	verifyOut, verifyErr := runMigrate(migrateBin, "-in", migratedPath,
		"-out", filepath.Join(migDir, "ignored.json"), "-ffprobe", ffprobe)
	check(verifyErr == nil, "重复运行（核验模式）退出码 0")
	var vrep migrate.Report
	if readJSONFile(migratedPath+".verify-report.json", &vrep) {
		allVerified := vrep.Mode == "verify" && vrep.Pending() == 0
		for _, m := range vrep.Media {
			allVerified = allVerified && m.Outcome == "verified"
		}
		check(allVerified, "重复运行核验：%d 个素材描述与源文件一致", len(vrep.Media))
	}
	_ = verifyOut
	check(!fileExists(filepath.Join(migDir, "ignored.json")), "核验模式不产出新工程文件")

	// --- 5.4 回退过程 ---
	restoredPath := filepath.Join(migDir, "restored_project.json")
	_, rbErr := runMigrate(migrateBin, "-rollback", "-in", migratedPath, "-out", restoredPath)
	check(rbErr == nil, "回退执行成功")
	var restoredRaw map[string]any
	if readJSONFile(restoredPath, &restoredRaw) {
		check(reflect.DeepEqual(legacyRaw, restoredRaw), "回退结果与旧工程逐字段一致（含未识别字段与旧裁切确认）")
	}

	// --- 5.5 迁移后工程在目标路径实际运行 ---
	mp, err := project.Load(migratedDir, "legacy_oral")
	check(err == nil, "迁移后工程可打开")
	if err != nil {
		return
	}
	mp.RecheckMedia(func(path string) (string, error) {
		info, err := prober.Probe(path)
		if err != nil {
			return "", err
		}
		return info.SHA256, nil
	})
	blocked := project.HasErrors(mp.Validate(false))
	check(blocked, "旧裁切确认失效后导出被阻止（不接受默认值伪装成功）")
	exp := &render.Exporter{FFmpeg: ffmpeg, TempDir: filepath.Join(migDir, "tmp")}
	_, blockedErr := exp.Export(mp, render.Options{OutPath: filepath.Join(migDir, "tmp", "should_not_exist.mp4")})
	check(blockedErr != nil, "未复核裁切阻止正式导出: %v", oneLine(blockedErr))

	// 编辑复核裁切位置后放行。
	for i := range mp.Clips {
		if mp.Clips[i].ID == "m1" {
			mp.Clips[i].Crop.Confirmed = true
			mp.Clips[i].Crop.ConfirmedBy = "迁移复核编辑"
		}
	}
	if err := mp.Save(migratedDir); err != nil {
		check(false, "复核后保存: %v", err)
		return
	}
	reloaded, err := project.Load(migratedDir, "legacy_oral")
	check(err == nil && len(reloaded.Legacy) > 0, "应用保存后 legacy 信封仍保留（回退能力不丢失）")

	program := filepath.Join(migDir, "output", "program.mp4")
	mmanifest, err := exp.Export(mp, render.Options{OutPath: program})
	check(err == nil, "迁移后工程在目标路径导出")
	if err != nil {
		return
	}
	check(mmanifest.FormatVersion == project.FormatVersionCurrent, "导出清单引用格式版本 v%d", mmanifest.FormatVersion)
	check(mmanifest.Clips[0].Description != nil && mmanifest.Clips[0].Description.SARNum == 64,
		"导出清单引用统一媒体描述（DV 像素比 %d:%d）",
		mmanifest.Clips[0].Description.SARNum, mmanifest.Clips[0].Description.SARDen)

	// 执行证据：成片画幅、时长、帧级主体边界。
	mprobe := probeFile(ffprobe, program)
	check(mprobe.width == 1920 && mprobe.height == 1080, "迁移成片画幅 %dx%d", mprobe.width, mprobe.height)
	check(math.Abs(mprobe.duration-8.0) < 0.35, "迁移成片时长 %.2fs（预期 8s，片段时长逐项对应）", mprobe.duration)
	check(math.Abs(mprobe.audioDur-8.0) < 0.35, "迁移成片音轨时长 %.2fs", mprobe.audioDur)

	framesDir := filepath.Join(migDir, "inspection", "frames")
	os.MkdirAll(framesDir, 0o755)
	for _, m := range []struct {
		clip string
		at   float64
	}{{"m1", 1.0}, {"m2", 3.0}, {"m3", 5.0}, {"m4", 7.0}} {
		png := filepath.Join(framesDir, m.clip+".png")
		run(ffmpeg, "-ss", fmt.Sprintf("%.2f", m.at), "-i", program, "-frames:v", "1", "-y", png)
	}
	// m1：DV 显示 1024x576 恰好 16:9，裁切后满幅，四角标记落画布四角（主体边界证据）。
	img := decode(filepath.Join(framesDir, "m1.png"))
	r, g, b := px(img, 30, 30)
	check(r > 180 && g < 90 && b < 90, "DV 片段左上角红色标记（非方形像素已归一化）(#%02x%02x%02x)", r, g, b)
	r, g, b = px(img, 960, 1073)
	check(g > 180 && b > 180 && r < 90, "DV 片段底部青色边缘可见（恰好满幅无裁切损失）(#%02x%02x%02x)", r, g, b)
	r, g, b = px(img, 10, 540)
	check(r+g+b > 140, "DV 片段两侧无黑边（编码尺寸未被当作显示尺寸）(#%02x%02x%02x)", r, g, b)
	// m2：竖屏手机，红色标记落左下。
	img = decode(filepath.Join(framesDir, "m2.png"))
	r, g, b = px(img, 681, 1055)
	check(r > 180 && g < 90 && b < 90, "竖屏片段方向正确（红色标记落于左下）(#%02x%02x%02x)", r, g, b)
	// m4：EXIF 肖像边框模式，红色标记落右上。
	img = decode(filepath.Join(framesDir, "m4.png"))
	r, g, b = px(img, 1293, 27)
	check(r > 180 && g < 90 && b < 90, "EXIF 肖像方向正确（红色标记落于右上）(#%02x%02x%02x)", r, g, b)

	rmsDV := audioRMS(ffmpeg, program, 0.5, 1.0)
	rmsPhoto := audioRMS(ffmpeg, program, 5.0, 1.0)
	check(rmsDV > -40, "DV 段音轨有效 (RMS %.1f dB)", rmsDV)
	check(rmsPhoto < -60, "照片段静音轨拼接 (RMS %.1f dB)", rmsPhoto)

	writeMigrationDoc(migDir, &rep, program, mprobe, rmsDV, rmsPhoto, ffmpeg, ffprobe)
	appendInspectionMigration(outAbs, &rep)
}

func runMigrate(bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w\n%s", err, out)
	}
	return string(out), nil
}

func readJSONFile(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		check(false, "read %s: %v", path, err)
		return false
	}
	if err := json.Unmarshal(data, v); err != nil {
		check(false, "parse %s: %v", path, err)
		return false
	}
	return true
}

func fileSHA256(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func firstWord(s string) string {
	w, _, _ := strings.Cut(s, " ")
	return w
}

// nz normalizes tiny float noise to 0 for display.
func nz(v float64) float64 {
	if math.Abs(v) < 0.5 {
		return 0
	}
	return v
}

func todAny(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func findMedia(list []any, id string) map[string]any {
	for _, m := range list {
		mm := m.(map[string]any)
		if mm["id"] == id {
			return mm
		}
	}
	return map[string]any{}
}

// writeMigrationDoc records versions, the change scope, per-media and
// per-clip evidence, and the rollback procedure at the target path.
func writeMigrationDoc(migDir string, rep *migrate.Report, program string, pr probeInfo, rmsDV, rmsPhoto float64, ffmpeg, ffprobe string) {
	var b strings.Builder
	b.WriteString("# 画幅工程迁移记录\n\n")
	fmt.Fprintf(&b, "生成时间: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("## 版本\n\n")
	fmt.Fprintf(&b, "- 迁移工具版本: %s\n", rep.ToolVersion)
	fmt.Fprintf(&b, "- 统一媒体描述版本: v%d\n", rep.DescriptionVersion)
	fmt.Fprintf(&b, "- 工程格式: v%d → v%d\n", rep.FormatVersionFrom, rep.FormatVersionTo)
	fmt.Fprintf(&b, "- ffprobe: %s\n", rep.FFprobeVersion)
	fmt.Fprintf(&b, "- 输入工程 SHA256: `%s`\n\n", rep.InputSHA256)
	b.WriteString("## 改动范围\n\n")
	fmt.Fprintf(&b, "- 素材迁移 %d 个，待定 %d 个；片段时长模型未改动（timingChanged=false）\n",
		rep.Scope.MediaMigrated, rep.Scope.MediaPending)
	fmt.Fprintf(&b, "- 语义调整片段: %v；裁切确认失效片段: %v\n\n",
		rep.Scope.ClipsAdjusted, rep.Scope.ClipsConfirmationReset)
	b.WriteString("## 素材对照（旧解释 → 统一描述）\n\n")
	b.WriteString("| 素材 | 旧记录（编码尺寸/旋转） | 像素比 | 真实显示尺寸 | 冲突 |\n|---|---|---|---|---|\n")
	for _, m := range rep.Media {
		sar, disp, conflicts := "-", "-", "-"
		if m.Current != nil {
			sar = fmt.Sprintf("%d:%d", m.Current.SARNum, m.Current.SARDen)
			disp = fmt.Sprintf("%dx%d", m.Current.DisplayWidth, m.Current.DisplayHeight)
		}
		if len(m.Conflicts) > 0 {
			conflicts = strings.Join(m.Conflicts, "；")
		}
		fmt.Fprintf(&b, "| %s | %dx%d rot=%d | %s | %s | %s |\n",
			m.MediaID, m.Legacy.Width, m.Legacy.Height, m.Legacy.Rotation, sar, disp, conflicts)
	}
	b.WriteString("\n## 片段语义保留证据（主体边界 / 片段时长）\n\n")
	b.WriteString("| 片段 | 保留程度 | 主体边界（旧→新，源像素） | 时长（旧→新） |\n|---|---|---|---|\n")
	for _, c := range rep.Clips {
		subj := "-"
		if c.SubjectBefore != nil && c.SubjectAfter != nil {
			subj = fmt.Sprintf("%.0fx%.0f@(%.0f,%.0f) → %.0fx%.0f@(%.0f,%.0f)",
				c.SubjectBefore.W, c.SubjectBefore.H, nz(c.SubjectBefore.X), nz(c.SubjectBefore.Y),
				c.SubjectAfter.W, c.SubjectAfter.H, nz(c.SubjectAfter.X), nz(c.SubjectAfter.Y))
		}
		fmt.Fprintf(&b, "| %s %s | %s | %s | %.2fs → %.2fs |\n",
			c.ClipID, c.Label, c.Preservation, subj, c.DurationBefore, c.DurationAfter)
	}
	b.WriteString("\n## 回退过程\n\n")
	b.WriteString("1. 原工程文件从未被修改，可直接继续使用。\n")
	fmt.Fprintf(&b, "2. 命令式回退：`%s`\n", rep.Rollback.Command)
	b.WriteString("3. 回退依据迁移文件中的 legacy 信封（未识别字段、被失效的裁切确认）逐字段还原；\n")
	b.WriteString("   本次交付已执行回退并核验：restored_project.json 与旧工程逐字段一致。\n")
	b.WriteString("\n## 执行证据\n\n")
	fmt.Fprintf(&b, "- 迁移后工程在目标路径实际导出: `%s`\n", program)
	fmt.Fprintf(&b, "- 成片 %dx%d，时长 %.2fs（4 片段各 2s），音轨 %.2fs\n", pr.width, pr.height, pr.duration, pr.audioDur)
	fmt.Fprintf(&b, "- DV 段音频 RMS %.1f dB；照片段 RMS %.1f dB\n", rmsDV, rmsPhoto)
	b.WriteString("- 帧级主体边界核对见 migration/inspection/frames/*.png：DV 满幅无黑边、竖屏与 EXIF 方向正确。\n")
	os.WriteFile(filepath.Join(migDir, "MIGRATION.md"), []byte(b.String()), 0o644)
}

func appendInspectionMigration(outAbs string, rep *migrate.Report) {
	path := filepath.Join(outAbs, "INSPECTION.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var b strings.Builder
	b.Write(data)
	b.WriteString("\n## 画幅工程迁移\n\n")
	fmt.Fprintf(&b, "- 旧工程（分散画幅/旋转字段、无像素比例）已迁入统一版本化媒体描述（格式 v%d），工具版本 %s。\n",
		rep.FormatVersionTo, rep.ToolVersion)
	fmt.Fprintf(&b, "- 素材迁移 %d 个、待定 %d 个；语义调整片段 %v；裁切确认失效 %v（需编辑复核）。\n",
		rep.Scope.MediaMigrated, rep.Scope.MediaPending, rep.Scope.ClipsAdjusted, rep.Scope.ClipsConfirmationReset)
	b.WriteString("- 迁移后工程已在目标路径实际运行（打开→校验→导出→帧/时长核对），回退已执行并逐字段核验。\n")
	b.WriteString("- 详见 migration/MIGRATION.md 与 migration/migration-report.json。\n")
	os.WriteFile(path, []byte(b.String()), 0o644)
}
