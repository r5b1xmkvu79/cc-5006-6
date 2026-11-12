// Package migrate converts legacy (v1) project files — where aspect,
// rotation and pixel ratio were scattered across flat fields and the
// pixel ratio was not recorded at all — into the unified versioned
// media description (format v2). The converter is repeatable, never
// modifies the input file, writes output atomically, preserves
// unrecognized fields for rollback, and refuses to fabricate geometry:
// media whose true display relationship cannot be derived from the
// source file are reported pending instead of being filled with
// defaults.
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"oralcut/internal/geometry"
	"oralcut/internal/media"
	"oralcut/internal/project"
)

// ToolVersion is recorded in every report and legacy envelope.
const ToolVersion = "1.0.0"

// durationEps is the tolerance for duration cross-checks (seconds).
const durationEps = 0.05

// subjectEps is the tolerance (source pixels) for deciding whether a
// clip's subject boundary survived migration unchanged.
const subjectEps = 0.5

// Options controls one Convert/Rollback run.
type Options struct {
	InPath  string
	OutPath string
	// ReportPath defaults to "<OutPath>.report.json" (convert) or
	// "<OutPath>.report.json" with mode rollback.
	ReportPath     string
	Prober         *media.Prober // required for Convert
	FFprobeVersion string        // recorded in the report
}

// LegacyGeometry echoes what the old file recorded, under the old
// interpretation: post-rotation coded dimensions, square pixels assumed.
type LegacyGeometry struct {
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	Rotation        int     `json:"rotation"`
	EXIFOrientation int     `json:"exifOrientation,omitempty"`
	Duration        float64 `json:"duration"`
	// AssumedDisplayWidth/Height are what the old interpretation showed:
	// the coded dimensions themselves.
	AssumedDisplayWidth  int `json:"assumedDisplayWidth"`
	AssumedDisplayHeight int `json:"assumedDisplayHeight"`
}

// MediaChange is the per-media migration record.
type MediaChange struct {
	MediaID string           `json:"mediaId"`
	Path    string           `json:"path"`
	Outcome string           `json:"outcome"` // "migrated" | "pending" | "verified" | "drift"
	Reason  string           `json:"reason,omitempty"`
	Legacy  LegacyGeometry   `json:"legacy"`
	Current *media.Description `json:"current,omitempty"`
	// Conflicts lists contradictions between the old record and the
	// probed source file. The description follows the file; the conflict
	// is reported, never silently resolved.
	Conflicts []string `json:"conflicts,omitempty"`
}

// ClipEvidence maps one clip's subject boundary and duration across the
// migration: the semantic preservation record.
type ClipEvidence struct {
	ClipID  string `json:"clipId"`
	MediaID string `json:"mediaId"`
	Label   string `json:"label"`
	Fit     string `json:"fit"`
	// DurationBefore/After are the clip's timeline seconds. Migration
	// never touches the time model; both are recorded as evidence.
	DurationBefore float64 `json:"durationBefore"`
	DurationAfter  float64 `json:"durationAfter"`
	// SubjectBefore/After are the visible source regions (source pixels)
	// under the old interpretation and the new description. After is nil
	// when the media is pending.
	SubjectBefore *geometry.Rect `json:"subjectBefore,omitempty"`
	SubjectAfter  *geometry.Rect `json:"subjectAfter,omitempty"`
	// Preservation: "preserved" (boundary unchanged), "adjusted"
	// (boundary shifted because the true display relationship differs),
	// or "pending" (media unresolved).
	Preservation string `json:"preservation"`
	Note         string `json:"note,omitempty"`
}

// ChangeScope summarizes what the migration touched, for review.
type ChangeScope struct {
	MediaMigrated          int      `json:"mediaMigrated"`
	MediaPending           int      `json:"mediaPending"`
	ClipsAdjusted          []string `json:"clipsAdjusted,omitempty"`
	ClipsConfirmationReset []string `json:"clipsConfirmationReset,omitempty"`
	TimingChanged          bool     `json:"timingChanged"`
}

// RollbackNote documents how to undo the migration.
type RollbackNote struct {
	Instructions string `json:"instructions"`
	Command      string `json:"command"`
}

// Report is the migration record: versions, per-media changes, per-clip
// evidence, preserved unknown fields, and rollback documentation.
type Report struct {
	Mode               string `json:"mode"` // "convert" | "verify" | "rollback"
	ToolVersion        string `json:"toolVersion"`
	DescriptionVersion int    `json:"descriptionVersion"`
	FormatVersionFrom  int    `json:"formatVersionFrom"`
	FormatVersionTo    int    `json:"formatVersionTo,omitempty"`
	FFprobeVersion     string `json:"ffprobeVersion,omitempty"`
	StartedAt          string `json:"startedAt"`
	FinishedAt         string `json:"finishedAt"`
	InputPath          string `json:"inputPath"`
	InputSHA256        string `json:"inputSha256"`
	OutputPath         string `json:"outputPath,omitempty"`

	Media         []MediaChange  `json:"media"`
	Clips         []ClipEvidence `json:"clips,omitempty"`
	UnknownFields []string       `json:"unknownFields,omitempty"`
	Scope         ChangeScope    `json:"scope"`
	Rollback      *RollbackNote  `json:"rollback,omitempty"`
}

// Pending counts media that could not be migrated truthfully.
func (r *Report) Pending() int { return r.Scope.MediaPending }

// legacyEnvelope is stored under "legacy" in the migrated file:
// everything needed to restore the old format faithfully.
type legacyEnvelope struct {
	FromFormatVersion int    `json:"fromFormatVersion"`
	ToolVersion       string `json:"toolVersion"`
	MigratedAt        string `json:"migratedAt"`
	// UnknownProjectFields / UnknownMediaFields hold raw JSON of fields
	// the converter does not model, keyed for exact restoration.
	UnknownProjectFields map[string]json.RawMessage            `json:"unknownProjectFields,omitempty"`
	UnknownMediaFields   map[string]map[string]json.RawMessage `json:"unknownMediaFields,omitempty"`
	// ClipOverrides preserves original clip fields the migration had to
	// change (crop confirmations invalidated by geometry shifts).
	ClipOverrides map[string]clipOverride `json:"clipOverrides,omitempty"`
}

type clipOverride struct {
	Crop project.Crop `json:"crop"`
}

func jsonKeys(v any) map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		keys[name] = true
	}
	return keys
}

var knownProjectKeys = jsonKeys(project.Project{})
var knownMediaKeys = jsonKeys(project.Media{})

// Convert migrates a legacy project file to format v2. Already-migrated
// files are verified instead (repeatable runs are safe). The input file
// is never modified; output is written atomically.
func Convert(opts Options) (*Report, error) {
	if opts.Prober == nil {
		return nil, fmt.Errorf("prober required")
	}
	inAbs, err := filepath.Abs(opts.InPath)
	if err != nil {
		return nil, err
	}
	rep := &Report{
		Mode: "convert", ToolVersion: ToolVersion,
		DescriptionVersion: media.DescriptionVersion,
		FFprobeVersion:     opts.FFprobeVersion,
		StartedAt:          time.Now().UTC().Format(time.RFC3339),
		InputPath:          inAbs,
	}
	data, err := os.ReadFile(inAbs)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	sum := sha256.Sum256(data)
	rep.InputSHA256 = hex.EncodeToString(sum[:])

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("input is not a JSON object: %w", err)
	}
	fromVersion := 1
	if raw, ok := top["formatVersion"]; ok {
		var v int
		if err := json.Unmarshal(raw, &v); err == nil && v > 0 {
			fromVersion = v
		}
	}
	rep.FormatVersionFrom = fromVersion

	if fromVersion >= project.FormatVersionCurrent {
		// Repeatable runs: verify recorded descriptions against the
		// source files instead of rewriting anything.
		rep.Mode = "verify"
		verify(top, opts.Prober, rep)
		rep.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if err := writeReport(reportPath(opts, ""), rep); err != nil {
			return rep, err
		}
		return rep, nil
	}

	outAbs, err := filepath.Abs(opts.OutPath)
	if err != nil {
		return nil, err
	}
	if outAbs == inAbs {
		return nil, fmt.Errorf("output path must differ from input: the original project is never overwritten")
	}
	out, err := convert(data, top, opts.Prober, rep)
	if err != nil {
		return nil, err
	}
	rep.FormatVersionTo = project.FormatVersionCurrent
	rep.OutputPath = outAbs
	rep.Rollback = &RollbackNote{
		Instructions: "回退：用迁移工具对迁移结果执行 -rollback，可依据 legacy 信封还原旧格式工程；原工程文件从未被修改，也可直接继续使用。",
		Command:      fmt.Sprintf("oralcut-migrate -rollback -in %s -out <restored.json>", outAbs),
	}
	if err := writeAtomic(outAbs, out); err != nil {
		return nil, fmt.Errorf("write output: %w", err)
	}
	rep.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeReport(reportPath(opts, outAbs), rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// convert performs the legacy -> v2 migration and returns the output
// document. rep is filled with the per-media and per-clip records.
func convert(data []byte, top map[string]json.RawMessage, prober *media.Prober, rep *Report) ([]byte, error) {
	var p project.Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decode legacy project: %w", err)
	}
	var rawMedia []map[string]json.RawMessage
	if raw, ok := top["media"]; ok {
		if err := json.Unmarshal(raw, &rawMedia); err != nil {
			return nil, fmt.Errorf("decode legacy media list: %w", err)
		}
	}

	env := &legacyEnvelope{
		FromFormatVersion: rep.FormatVersionFrom,
		ToolVersion:       ToolVersion,
		MigratedAt:        rep.StartedAt,
	}
	// Unrecognized top-level fields are preserved verbatim for rollback.
	for k, v := range top {
		if !knownProjectKeys[k] {
			if env.UnknownProjectFields == nil {
				env.UnknownProjectFields = map[string]json.RawMessage{}
			}
			env.UnknownProjectFields[k] = v
			rep.UnknownFields = append(rep.UnknownFields, k)
		}
	}

	// Legacy display interpretation per media, captured before any
	// mirroring: post-rotation coded dimensions, square pixels assumed.
	type legacyGeom struct{ w, h int }
	legacy := map[string]legacyGeom{}
	for i := range p.Media {
		m := &p.Media[i]
		legacy[m.ID] = legacyGeom{m.Width, m.Height}
		change := MediaChange{
			MediaID: m.ID, Path: m.Path,
			Legacy: LegacyGeometry{
				Width: m.Width, Height: m.Height, Rotation: m.Rotation,
				EXIFOrientation: m.EXIFOrientation, Duration: m.Duration,
				AssumedDisplayWidth: m.Width, AssumedDisplayHeight: m.Height,
			},
		}
		// Unrecognized media fields are preserved verbatim.
		if i < len(rawMedia) {
			for k, v := range rawMedia[i] {
				if !knownMediaKeys[k] {
					if env.UnknownMediaFields == nil {
						env.UnknownMediaFields = map[string]map[string]json.RawMessage{}
					}
					if env.UnknownMediaFields[m.ID] == nil {
						env.UnknownMediaFields[m.ID] = map[string]json.RawMessage{}
					}
					env.UnknownMediaFields[m.ID][k] = v
					rep.UnknownFields = append(rep.UnknownFields, "media."+m.ID+"."+k)
				}
			}
		}

		info, err := prober.Probe(m.Path)
		if err != nil {
			// No truthful derivation is possible: leave the media
			// without a description and say so. Defaults are never
			// substituted for a real probe.
			change.Outcome = "pending"
			change.Reason = fmt.Sprintf("无法从源文件推导显示关系: %v", err)
			rep.Scope.MediaPending++
			rep.Media = append(rep.Media, change)
			continue
		}
		change.Conflicts = crossCheck(m, info)
		desc := media.DescriptionFromInfo(info)
		m.Description = desc
		change.Outcome = "migrated"
		change.Current = desc
		rep.Scope.MediaMigrated++
		rep.Media = append(rep.Media, change)
	}

	// Per-clip semantic preservation evidence: subject boundary under the
	// old interpretation vs the new description, plus duration. The time
	// model is never touched.
	for ci := range p.Clips {
		c := &p.Clips[ci]
		ev := ClipEvidence{
			ClipID: c.ID, MediaID: c.MediaID, Label: c.Label, Fit: string(c.Fit),
			DurationBefore: c.Duration(), DurationAfter: c.Duration(),
		}
		m := p.MediaByID(c.MediaID)
		switch {
		case m == nil:
			ev.Preservation = "pending"
			ev.Note = "片段引用的素材不存在"
		case m.Description == nil:
			ev.Preservation = "pending"
			ev.Note = "素材描述待定（源文件不可推导）"
		default:
			lg := legacy[m.ID]
			ev.SubjectBefore = subjectBoundary(&p, c, float64(lg.w), float64(lg.h))
			dw, dh := m.Description.DisplayWidth, m.Description.DisplayHeight
			ev.SubjectAfter = subjectBoundary(&p, c, float64(dw), float64(dh))
			if rectsClose(ev.SubjectBefore, ev.SubjectAfter) {
				ev.Preservation = "preserved"
			} else {
				ev.Preservation = "adjusted"
				ev.Note = "真实显示关系与旧解释不同，主体边界已按新描述重算"
				rep.Scope.ClipsAdjusted = append(rep.Scope.ClipsAdjusted, c.ID)
				// A crop confirmed under the old interpretation no
				// longer covers the true framing: the confirmation is
				// invalidated (original kept in the legacy envelope) so
				// nothing silently passes as still-approved.
				if c.Fit == geometry.FitCrop && c.Crop.Confirmed {
					if env.ClipOverrides == nil {
						env.ClipOverrides = map[string]clipOverride{}
					}
					env.ClipOverrides[c.ID] = clipOverride{Crop: c.Crop}
					c.Crop.Confirmed = false
					c.Crop.ConfirmedBy = ""
					ev.Note = "主体边界变化，旧裁切确认已失效并保留于 legacy 信封，需编辑复核"
					rep.Scope.ClipsConfirmationReset = append(rep.Scope.ClipsConfirmationReset, c.ID)
				}
			}
		}
		rep.Clips = append(rep.Clips, ev)
	}
	sort.Strings(rep.UnknownFields)

	envData, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	p.Legacy = envData
	p.FormatVersion = project.FormatVersionCurrent
	p.NormalizeForSave()
	return json.MarshalIndent(&p, "", "  ")
}

// verify re-derives descriptions from the source files and compares them
// against an already-migrated project. Nothing is rewritten.
func verify(top map[string]json.RawMessage, prober *media.Prober, rep *Report) {
	data, _ := json.Marshal(top)
	var p project.Project
	if err := json.Unmarshal(data, &p); err != nil {
		rep.Media = append(rep.Media, MediaChange{Outcome: "drift", Reason: "工程文件无法解析: " + err.Error()})
		rep.Scope.MediaPending++
		return
	}
	for i := range p.Media {
		m := &p.Media[i]
		change := MediaChange{MediaID: m.ID, Path: m.Path}
		if m.Description == nil {
			change.Outcome = "pending"
			change.Reason = "缺少媒体描述"
			rep.Scope.MediaPending++
			rep.Media = append(rep.Media, change)
			continue
		}
		info, err := prober.Probe(m.Path)
		if err != nil {
			change.Outcome = "pending"
			change.Reason = fmt.Sprintf("无法从源文件复核: %v", err)
			rep.Scope.MediaPending++
			rep.Media = append(rep.Media, change)
			continue
		}
		want := media.DescriptionFromInfo(info)
		change.Current = want
		if descriptionsEqual(m.Description, want) {
			change.Outcome = "verified"
			rep.Scope.MediaMigrated++
		} else {
			change.Outcome = "drift"
			change.Reason = descriptionsDiff(m.Description, want)
			rep.Scope.MediaPending++
		}
		rep.Media = append(rep.Media, change)
	}
}

// Rollback restores the legacy (v1) project format from a migrated file,
// using the preserved legacy envelope. The input is never modified.
func Rollback(opts Options) (*Report, error) {
	inAbs, err := filepath.Abs(opts.InPath)
	if err != nil {
		return nil, err
	}
	rep := &Report{
		Mode: "rollback", ToolVersion: ToolVersion,
		DescriptionVersion: media.DescriptionVersion,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		InputPath: inAbs,
	}
	data, err := os.ReadFile(inAbs)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	sum := sha256.Sum256(data)
	rep.InputSHA256 = hex.EncodeToString(sum[:])

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("input is not a JSON object: %w", err)
	}
	var version int
	if raw, ok := top["formatVersion"]; !ok {
		return nil, fmt.Errorf("not a migrated project: no formatVersion")
	} else if err := json.Unmarshal(raw, &version); err != nil || version < project.FormatVersionCurrent {
		return nil, fmt.Errorf("not a migrated project: formatVersion=%d", version)
	}
	rep.FormatVersionFrom = version
	rawEnv, ok := top["legacy"]
	if !ok {
		return nil, fmt.Errorf("no legacy envelope: faithful rollback impossible")
	}
	var env legacyEnvelope
	if err := json.Unmarshal(rawEnv, &env); err != nil {
		return nil, fmt.Errorf("legacy envelope corrupt: %w", err)
	}

	outAbs, err := filepath.Abs(opts.OutPath)
	if err != nil {
		return nil, err
	}
	if outAbs == inAbs {
		return nil, fmt.Errorf("output path must differ from input")
	}

	// Media: restore legacy flat geometry from the description, drop the
	// description, and merge preserved unknown fields back in place.
	var rawMedia []map[string]json.RawMessage
	if raw, ok := top["media"]; ok {
		if err := json.Unmarshal(raw, &rawMedia); err != nil {
			return nil, fmt.Errorf("decode media list: %w", err)
		}
	}
	for _, rm := range rawMedia {
		var id string
		if raw, ok := rm["id"]; ok {
			json.Unmarshal(raw, &id)
		}
		change := MediaChange{MediaID: id, Outcome: "migrated"}
		if raw, ok := rm["description"]; ok {
			var d media.Description
			if err := json.Unmarshal(raw, &d); err != nil {
				return nil, fmt.Errorf("media %s: description corrupt: %w", id, err)
			}
			// Legacy contract: width/height were post-rotation coded
			// dimensions under a square-pixel assumption.
			w, h := geometry.NormalizedSize(d.StoredWidth, d.StoredHeight, d.Rotation)
			setNumber(rm, "width", w)
			setNumber(rm, "height", h)
			setNumber(rm, "rotation", d.Rotation)
			if d.EXIFOrientation > 0 {
				setNumber(rm, "exifOrientation", d.EXIFOrientation)
			} else {
				delete(rm, "exifOrientation")
			}
			setNumber(rm, "duration", d.Duration)
			setBool(rm, "hasAudio", d.HasAudio)
			delete(rm, "description")
			change.Legacy = LegacyGeometry{
				Width: w, Height: h, Rotation: d.Rotation,
				EXIFOrientation: d.EXIFOrientation, Duration: d.Duration,
				AssumedDisplayWidth: w, AssumedDisplayHeight: h,
			}
		} else {
			change.Reason = "无描述（迁移时即为待定），保持旧字段原样"
		}
		for k, v := range env.UnknownMediaFields[id] {
			rm[k] = v
		}
		rep.Media = append(rep.Media, change)
		rep.Scope.MediaMigrated++
	}
	if raw, ok := top["media"]; ok && len(rawMedia) > 0 {
		_ = raw
		merged, err := json.Marshal(rawMedia)
		if err != nil {
			return nil, err
		}
		top["media"] = merged
	}

	// Clips: restore overridden fields (crop confirmations).
	if raw, ok := top["clips"]; ok {
		var rawClips []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rawClips); err != nil {
			return nil, fmt.Errorf("decode clips: %w", err)
		}
		for _, rc := range rawClips {
			var id string
			if raw, ok := rc["id"]; ok {
				json.Unmarshal(raw, &id)
			}
			if ov, ok := env.ClipOverrides[id]; ok {
				cropData, err := json.Marshal(ov.Crop)
				if err != nil {
					return nil, err
				}
				rc["crop"] = cropData
			}
		}
		merged, err := json.Marshal(rawClips)
		if err != nil {
			return nil, err
		}
		top["clips"] = merged
	}

	// Top level: drop migration artifacts, restore unknown fields and the
	// original format version (absent for v1).
	delete(top, "legacy")
	delete(top, "formatVersion")
	if env.FromFormatVersion > 1 {
		v, _ := json.Marshal(env.FromFormatVersion)
		top["formatVersion"] = v
	}
	for k, v := range env.UnknownProjectFields {
		top[k] = v
	}

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(outAbs, out); err != nil {
		return nil, fmt.Errorf("write output: %w", err)
	}
	rep.FormatVersionTo = env.FromFormatVersion
	rep.OutputPath = outAbs
	rep.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeReport(reportPath(opts, outAbs), rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// crossCheck compares the legacy record against the probed truth.
func crossCheck(m *project.Media, info *media.Info) []string {
	var conflicts []string
	// Legacy width/height were post-rotation coded dimensions.
	cw, ch := m.Width, m.Height
	if m.Rotation == 90 || m.Rotation == 270 {
		cw, ch = m.Height, m.Width
	}
	if cw != info.StoredWidth || ch != info.StoredHeight {
		conflicts = append(conflicts, fmt.Sprintf(
			"旧记录编码尺寸 %dx%d（去旋转后）与源文件 %dx%d 不一致", cw, ch, info.StoredWidth, info.StoredHeight))
	}
	if m.Rotation != info.Rotation {
		conflicts = append(conflicts, fmt.Sprintf(
			"旧记录旋转 %d° 与源文件 %d° 不一致", m.Rotation, info.Rotation))
	}
	if m.EXIFOrientation != info.EXIFOrientation {
		conflicts = append(conflicts, fmt.Sprintf(
			"旧记录 EXIF 方向 %d 与源文件 %d 不一致", m.EXIFOrientation, info.EXIFOrientation))
	}
	if math.Abs(m.Duration-info.Duration) > durationEps {
		conflicts = append(conflicts, fmt.Sprintf(
			"旧记录时长 %.3fs 与源文件 %.3fs 不一致", m.Duration, info.Duration))
	}
	if m.SHA256 != "" && m.SHA256 != info.SHA256 {
		conflicts = append(conflicts, "源文件内容哈希与旧记录不一致（文件已变化）")
	}
	return conflicts
}

// subjectBoundary computes the visible source region for a clip under
// the given source dimensions.
func subjectBoundary(p *project.Project, c *project.Clip, sw, sh float64) *geometry.Rect {
	l, err := geometry.LayoutFor(sw, sh,
		float64(p.Canvas.Width), float64(p.Canvas.Height), c.Fit, c.Crop.X, c.Crop.Y)
	if err != nil {
		return nil
	}
	r := l.SrcVisible
	return &r
}

func rectsClose(a, b *geometry.Rect) bool {
	if a == nil || b == nil {
		return a == b
	}
	return math.Abs(a.X-b.X) <= subjectEps && math.Abs(a.Y-b.Y) <= subjectEps &&
		math.Abs(a.W-b.W) <= subjectEps && math.Abs(a.H-b.H) <= subjectEps
}

func descriptionsEqual(a, b *media.Description) bool {
	return a.Version == b.Version &&
		a.StoredWidth == b.StoredWidth && a.StoredHeight == b.StoredHeight &&
		a.Rotation == b.Rotation && a.EXIFOrientation == b.EXIFOrientation &&
		a.SARNum == b.SARNum && a.SARDen == b.SARDen &&
		a.DisplayWidth == b.DisplayWidth && a.DisplayHeight == b.DisplayHeight &&
		math.Abs(a.Duration-b.Duration) <= durationEps &&
		a.HasAudio == b.HasAudio
}

func descriptionsDiff(a, b *media.Description) string {
	var parts []string
	f := func(name string, av, bv any) {
		if av != bv {
			parts = append(parts, fmt.Sprintf("%s 记录值 %v ≠ 探测值 %v", name, av, bv))
		}
	}
	f("storedWidth", a.StoredWidth, b.StoredWidth)
	f("storedHeight", a.StoredHeight, b.StoredHeight)
	f("rotation", a.Rotation, b.Rotation)
	f("exifOrientation", a.EXIFOrientation, b.EXIFOrientation)
	f("sarNum", a.SARNum, b.SARNum)
	f("sarDen", a.SARDen, b.SARDen)
	f("displayWidth", a.DisplayWidth, b.DisplayWidth)
	f("displayHeight", a.DisplayHeight, b.DisplayHeight)
	f("hasAudio", a.HasAudio, b.HasAudio)
	if math.Abs(a.Duration-b.Duration) > durationEps {
		parts = append(parts, fmt.Sprintf("duration 记录值 %.3f ≠ 探测值 %.3f", a.Duration, b.Duration))
	}
	if a.Version != b.Version {
		parts = append(parts, fmt.Sprintf("description 版本 %d ≠ 当前 %d", a.Version, b.Version))
	}
	return strings.Join(parts, "; ")
}

func setNumber(m map[string]json.RawMessage, key string, v any) {
	data, _ := json.Marshal(v)
	m[key] = data
}

func setBool(m map[string]json.RawMessage, key string, v bool) {
	data, _ := json.Marshal(v)
	m[key] = data
}

// writeAtomic writes data to a temp file in the same directory and
// renames it into place, so an interrupted run never leaves a partial
// project at the target path.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".migrate-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func reportPath(opts Options, outAbs string) string {
	if opts.ReportPath != "" {
		return opts.ReportPath
	}
	if outAbs != "" {
		return outAbs + ".report.json"
	}
	return opts.InPath + ".verify-report.json"
}

func writeReport(path string, rep *Report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}
