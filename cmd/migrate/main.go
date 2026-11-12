// Command oralcut-migrate converts legacy (v1) project files — scattered
// aspect/rotation fields, no pixel ratio — into the unified versioned
// media description (format v2), verifies already-migrated files on
// repeat runs, and rolls migrated files back to the legacy format.
//
// Exit codes: 0 = complete (or verified), 1 = hard error,
// 3 = incomplete (media pending or drift detected). The tool never
// reports success on defaulted geometry: media that cannot be probed are
// reported pending and the exit code says so.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"oralcut/internal/migrate"
	"oralcut/internal/media"
)

func main() {
	in := flag.String("in", "", "input project JSON (legacy v1, or v2 to verify)")
	out := flag.String("out", "", "output project JSON (convert/rollback)")
	report := flag.String("report", "", "report JSON path (default: <out>.report.json)")
	rollback := flag.Bool("rollback", false, "restore the legacy format from a migrated file")
	ffprobe := flag.String("ffprobe", os.Getenv("ORALCUT_FFPROBE"), "ffprobe binary")
	flag.Parse()

	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: oralcut-migrate -in old.json -out new.json [-report r.json] [-rollback]")
		os.Exit(2)
	}
	if *ffprobe == "" {
		fmt.Fprintln(os.Stderr, "ffprobe required (-ffprobe or ORALCUT_FFPROBE)")
		os.Exit(2)
	}

	opts := migrate.Options{
		InPath: *in, OutPath: *out, ReportPath: *report,
		Prober:         media.NewProber(*ffprobe),
		FFprobeVersion: ffprobeVersion(*ffprobe),
	}

	var rep *migrate.Report
	var err error
	if *rollback {
		rep, err = migrate.Rollback(opts)
	} else {
		rep, err = migrate.Convert(opts)
	}
	if rep != nil {
		printSummary(rep)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	if rep.Pending() > 0 {
		os.Exit(3)
	}
}

func printSummary(rep *migrate.Report) {
	fmt.Printf("模式: %s  工具版本: %s  描述版本: v%d  格式: v%d",
		rep.Mode, rep.ToolVersion, rep.DescriptionVersion, rep.FormatVersionFrom)
	if rep.FormatVersionTo > 0 {
		fmt.Printf(" -> v%d", rep.FormatVersionTo)
	}
	fmt.Println()
	for _, m := range rep.Media {
		line := fmt.Sprintf("  [%s] %s", m.Outcome, m.MediaID)
		if m.Current != nil {
			d := m.Current
			line += fmt.Sprintf("  编码 %dx%d  旋转 %d°  像素比 %d:%d  显示 %dx%d",
				d.StoredWidth, d.StoredHeight, d.Rotation, d.SARNum, d.SARDen,
				d.DisplayWidth, d.DisplayHeight)
		}
		if m.Reason != "" {
			line += "  " + m.Reason
		}
		fmt.Println(line)
		for _, c := range m.Conflicts {
			fmt.Println("    冲突:", c)
		}
	}
	for _, c := range rep.Clips {
		fmt.Printf("  片段 %s (%s): 语义%s  时长 %.2fs -> %.2fs\n",
			c.ClipID, c.Label, c.Preservation, c.DurationBefore, c.DurationAfter)
		if c.Note != "" {
			fmt.Println("    ", c.Note)
		}
	}
	if rep.Scope.MediaPending > 0 {
		fmt.Printf("未完成: %d 个素材待定（未以默认值冒充成功）\n", rep.Scope.MediaPending)
	}
	if rep.OutputPath != "" {
		fmt.Println("输出:", rep.OutputPath)
	}
}

func ffprobeVersion(bin string) string {
	out, err := exec.Command(bin, "-version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}
