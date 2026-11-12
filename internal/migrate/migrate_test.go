package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"oralcut/internal/geometry"
	"oralcut/internal/media"
	"oralcut/internal/project"
)

func testProber(t *testing.T) *media.Prober {
	t.Helper()
	ffprobe := os.Getenv("ORALCUT_FFPROBE")
	if ffprobe == "" {
		t.Skip("ORALCUT_FFPROBE not set")
	}
	return media.NewProber(ffprobe)
}

func assetPath(t *testing.T, name string) string {
	t.Helper()
	dir := os.Getenv("ORALCUT_ASSETS")
	if dir == "" {
		dir = "../../assets"
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("asset %s not generated: %v", name, err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// legacyMedia builds a media record as the OLD format stored it:
// post-rotation coded dimensions, rotation/EXIF scattered in flat fields,
// no pixel ratio anywhere, and no unified description.
func legacyMedia(t *testing.T, prober *media.Prober, id, path string) project.Media {
	t.Helper()
	info, err := prober.Probe(path)
	if err != nil {
		t.Fatalf("probe %s: %v", path, err)
	}
	return project.Media{
		ID: id, Path: info.Path, SHA256: info.SHA256,
		Kind: string(info.Kind), Width: info.Width, Height: info.Height,
		Rotation: info.Rotation, EXIFOrientation: info.EXIFOrientation,
		Duration: info.Duration, HasAudio: info.HasAudio, Title: info.Title,
		Status: project.StatusOK,
	}
}

// writeLegacyFixture writes a v1 project file (no formatVersion, no
// descriptions) with unknown fields sprinkled in, and returns its path
// plus the raw map for later comparison.
func writeLegacyFixture(t *testing.T, prober *media.Prober, dir string) (string, map[string]any) {
	t.Helper()
	a := func(name string) string { return assetPath(t, name) }
	p := project.New("legacy_oral", "口述纪录旧工程（画幅迁移样例）")
	p.FormatVersion = 0 // legacy files carry no formatVersion
	p.Media = []project.Media{
		legacyMedia(t, prober, "dv", a("dv_anamorphic.mp4")),
		legacyMedia(t, prober, "phone", a("phone_rotated.mp4")),
		legacyMedia(t, prober, "photo", a("photo_landscape.jpg")),
		legacyMedia(t, prober, "exifportrait", a("photo_portrait_exif6.jpg")),
	}
	confirmed := project.Crop{X: 0.5, Y: 0, Confirmed: true, ConfirmedBy: "旧编辑"}
	p.Clips = []project.Clip{
		{ID: "m1", MediaID: "dv", Kind: project.ClipVideo, Label: "DV宽屏访谈",
			In: 0, Out: 2, Fit: geometry.FitCrop, Crop: confirmed},
		{ID: "m2", MediaID: "phone", Kind: project.ClipVideo, Label: "竖屏手机访谈",
			In: 0, Out: 2, Fit: geometry.FitFull},
		{ID: "m3", MediaID: "photo", Kind: project.ClipPhoto, Label: "静态照片",
			Dwell: 2, Fit: geometry.FitFull},
		{ID: "m4", MediaID: "exifportrait", Kind: project.ClipPhoto, Label: "EXIF肖像",
			Dwell: 2, Fit: geometry.FitBorder, Border: project.Border{Blur: 12}},
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	// Fields the converter does not model: must be preserved for rollback.
	raw["archiveShelfmark"] = "城南照相馆-架A-07"
	raw["media"].([]any)[0].(map[string]any)["legacyTapeNo"] = "DV-1998-042"
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "legacy_project.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func shaOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func mediaByID(t *testing.T, raw map[string]any, id string) map[string]any {
	t.Helper()
	for _, m := range raw["media"].([]any) {
		mm := m.(map[string]any)
		if mm["id"] == id {
			return mm
		}
	}
	t.Fatalf("media %s not found", id)
	return nil
}

func TestConvertLegacyProject(t *testing.T) {
	prober := testProber(t)
	dir := t.TempDir()
	legacyPath, _ := writeLegacyFixture(t, prober, dir)
	before := shaOf(t, legacyPath)

	out := filepath.Join(dir, "migrated", "project.json")
	rep, err := Convert(Options{InPath: legacyPath, OutPath: out, Prober: prober, FFprobeVersion: "test-ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", rep.Pending())
	}
	if rep.Scope.MediaMigrated != 4 {
		t.Errorf("migrated = %d, want 4", rep.Scope.MediaMigrated)
	}
	if rep.Scope.TimingChanged {
		t.Error("migration must never touch clip timing")
	}

	// The original project is never modified: an interrupted or repeated
	// run still finds it intact and usable.
	if shaOf(t, legacyPath) != before {
		t.Error("input file modified by conversion")
	}

	raw := readJSON(t, out)
	if raw["formatVersion"].(float64) != 2 {
		t.Errorf("formatVersion = %v", raw["formatVersion"])
	}

	// Non-square pixels: coded 720x576 must NOT become display size.
	dv := mediaByID(t, raw, "dv")
	desc := dv["description"].(map[string]any)
	if desc["displayWidth"].(float64) != 1024 || desc["displayHeight"].(float64) != 576 {
		t.Errorf("dv display = %vx%v, want 1024x576", desc["displayWidth"], desc["displayHeight"])
	}
	if desc["sarNum"].(float64) != 64 || desc["sarDen"].(float64) != 45 {
		t.Errorf("dv sar = %v:%v", desc["sarNum"], desc["sarDen"])
	}
	if desc["storedWidth"].(float64) != 720 {
		t.Errorf("dv stored = %v", desc["storedWidth"])
	}
	// Compat mirror carries display dims for older readers.
	if dv["width"].(float64) != 1024 {
		t.Errorf("dv compat width = %v, want 1024", dv["width"])
	}

	// Portrait phone video: rotation preserved, display 720x1280.
	phone := mediaByID(t, raw, "phone")["description"].(map[string]any)
	if phone["displayWidth"].(float64) != 720 || phone["displayHeight"].(float64) != 1280 {
		t.Errorf("phone display = %vx%v", phone["displayWidth"], phone["displayHeight"])
	}
	if phone["rotation"].(float64) != 90 {
		t.Errorf("phone rotation = %v", phone["rotation"])
	}

	// Unknown fields preserved in the legacy envelope for rollback.
	env := raw["legacy"].(map[string]any)
	if env["unknownProjectFields"].(map[string]any)["archiveShelfmark"] != "城南照相馆-架A-07" {
		t.Error("unknown top-level field not preserved")
	}
	if env["unknownMediaFields"].(map[string]any)["dv"].(map[string]any)["legacyTapeNo"] != "DV-1998-042" {
		t.Error("unknown media field not preserved")
	}

	// Subject boundary evidence: the DV crop clip shifted from a 720-wide
	// coded window to the full 1024-wide display window.
	var m1, m2 *ClipEvidence
	for i := range rep.Clips {
		switch rep.Clips[i].ClipID {
		case "m1":
			m1 = &rep.Clips[i]
		case "m2":
			m2 = &rep.Clips[i]
		}
	}
	if m1 == nil || m1.Preservation != "adjusted" {
		t.Fatalf("m1 preservation = %+v", m1)
	}
	if m1.SubjectBefore.W != 720 || m1.SubjectAfter.W != 1024 {
		t.Errorf("m1 subject width %.0f -> %.0f, want 720 -> 1024",
			m1.SubjectBefore.W, m1.SubjectAfter.W)
	}
	if m1.DurationBefore != 2 || m1.DurationAfter != 2 {
		t.Errorf("m1 duration changed: %v -> %v", m1.DurationBefore, m1.DurationAfter)
	}
	if m2 == nil || m2.Preservation != "preserved" {
		t.Errorf("m2 preservation = %+v", m2)
	}

	// The old crop confirmation no longer covers the true framing: it is
	// invalidated, not carried over as if still approved.
	clips := raw["clips"].([]any)
	c1 := clips[0].(map[string]any)
	if c1["crop"].(map[string]any)["confirmed"] != false {
		t.Error("stale crop confirmation must be reset on geometry shift")
	}
	ov := env["clipOverrides"].(map[string]any)["m1"].(map[string]any)["crop"].(map[string]any)
	if ov["confirmed"] != true || ov["confirmedBy"] != "旧编辑" {
		t.Error("original crop confirmation not preserved in legacy envelope")
	}

	// Report records versions and rollback documentation.
	if rep.ToolVersion != ToolVersion || rep.DescriptionVersion != media.DescriptionVersion {
		t.Error("versions not recorded")
	}
	if rep.Rollback == nil || !strings.Contains(rep.Rollback.Command, "-rollback") {
		t.Error("rollback documentation missing")
	}
	if _, err := os.Stat(out + ".report.json"); err != nil {
		t.Error("report file not written")
	}
}

func TestConvertIsRepeatable(t *testing.T) {
	prober := testProber(t)
	dir := t.TempDir()
	legacyPath, _ := writeLegacyFixture(t, prober, dir)
	out := filepath.Join(dir, "migrated.json")
	if _, err := Convert(Options{InPath: legacyPath, OutPath: out, Prober: prober}); err != nil {
		t.Fatal(err)
	}
	first := shaOf(t, out)

	// Second run on the migrated file: verify mode, nothing rewritten.
	rep, err := Convert(Options{InPath: out, OutPath: filepath.Join(dir, "ignored.json"), Prober: prober})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "verify" {
		t.Fatalf("mode = %s, want verify", rep.Mode)
	}
	if rep.Pending() != 0 {
		t.Errorf("verify pending = %d", rep.Pending())
	}
	for _, m := range rep.Media {
		if m.Outcome != "verified" {
			t.Errorf("%s outcome = %s (%s)", m.MediaID, m.Outcome, m.Reason)
		}
	}
	if shaOf(t, out) != first {
		t.Error("verify run must not rewrite the migrated file")
	}
	if _, err := os.Stat(filepath.Join(dir, "ignored.json")); !os.IsNotExist(err) {
		t.Error("verify run must not write an output file")
	}
}

func TestConvertPendingNeverFakesSuccess(t *testing.T) {
	prober := testProber(t)
	dir := t.TempDir()
	legacyPath, _ := writeLegacyFixture(t, prober, dir)
	raw := readJSON(t, legacyPath)
	// Point one media at a file that does not exist.
	raw["media"].([]any)[1].(map[string]any)["path"] = filepath.Join(dir, "gone.mp4")
	data, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "migrated.json")
	rep, err := Convert(Options{InPath: legacyPath, OutPath: out, Prober: prober})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pending() != 1 {
		t.Fatalf("pending = %d, want 1", rep.Pending())
	}
	// The pending media must NOT carry a description: no defaults are
	// substituted for a real probe.
	migrated := readJSON(t, out)
	phone := mediaByID(t, migrated, "phone")
	if _, ok := phone["description"]; ok {
		t.Error("pending media must not get a fabricated description")
	}
	// Its legacy fields stay as recorded, so the project still opens and
	// rollback stays faithful.
	if phone["width"].(float64) != 720 || phone["height"].(float64) != 1280 {
		t.Error("pending media legacy fields not preserved")
	}
	// The pending clip is reported pending, not preserved.
	var found bool
	for _, c := range rep.Clips {
		if c.ClipID == "m2" {
			found = true
			if c.Preservation != "pending" {
				t.Errorf("m2 preservation = %s", c.Preservation)
			}
			if c.SubjectAfter != nil {
				t.Error("pending clip must not have a derived subject boundary")
			}
		}
	}
	if !found {
		t.Error("m2 evidence missing")
	}
}

func TestConvertReportsConflicts(t *testing.T) {
	prober := testProber(t)
	dir := t.TempDir()
	legacyPath, _ := writeLegacyFixture(t, prober, dir)
	raw := readJSON(t, legacyPath)
	// The old record lies about the phone rotation.
	raw["media"].([]any)[1].(map[string]any)["rotation"] = float64(0)
	data, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := Convert(Options{InPath: legacyPath, OutPath: filepath.Join(dir, "m.json"), Prober: prober})
	if err != nil {
		t.Fatal(err)
	}
	var phone *MediaChange
	for i := range rep.Media {
		if rep.Media[i].MediaID == "phone" {
			phone = &rep.Media[i]
		}
	}
	if phone == nil || len(phone.Conflicts) == 0 {
		t.Fatal("rotation conflict not reported")
	}
	// The description follows the probed truth, not the false record.
	if phone.Current.Rotation != 90 {
		t.Errorf("description rotation = %d, want probed 90", phone.Current.Rotation)
	}
}

func TestRollbackRoundTrip(t *testing.T) {
	prober := testProber(t)
	dir := t.TempDir()
	legacyPath, legacyRaw := writeLegacyFixture(t, prober, dir)

	migrated := filepath.Join(dir, "migrated.json")
	if _, err := Convert(Options{InPath: legacyPath, OutPath: migrated, Prober: prober}); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored.json")
	rep, err := Rollback(Options{InPath: migrated, OutPath: restored})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "rollback" {
		t.Errorf("mode = %s", rep.Mode)
	}

	restoredRaw := readJSON(t, restored)
	if !reflect.DeepEqual(legacyRaw, restoredRaw) {
		a, _ := json.MarshalIndent(legacyRaw, "", "  ")
		b, _ := json.MarshalIndent(restoredRaw, "", "  ")
		t.Errorf("rollback did not restore the legacy project\n--- original ---\n%s\n--- restored ---\n%s", a, b)
	}
}

func TestConvertFailureKeepsOriginalUsable(t *testing.T) {
	prober := testProber(t)
	dir := t.TempDir()
	legacyPath, _ := writeLegacyFixture(t, prober, dir)
	before := shaOf(t, legacyPath)

	// Unwritable output location: a path through a regular file.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Convert(Options{InPath: legacyPath, OutPath: filepath.Join(blocker, "out.json"), Prober: prober})
	if err == nil {
		t.Fatal("conversion into an unwritable path must fail")
	}
	if shaOf(t, legacyPath) != before {
		t.Error("failed conversion modified the original project")
	}
	// No partial output or temp residue.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".migrate-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	// Same-path output is refused outright.
	if _, err := Convert(Options{InPath: legacyPath, OutPath: legacyPath, Prober: prober}); err == nil {
		t.Error("in == out must be refused")
	}
}

func TestVerifyDetectsDrift(t *testing.T) {
	prober := testProber(t)
	dir := t.TempDir()
	legacyPath, _ := writeLegacyFixture(t, prober, dir)
	migrated := filepath.Join(dir, "migrated.json")
	if _, err := Convert(Options{InPath: legacyPath, OutPath: migrated, Prober: prober}); err != nil {
		t.Fatal(err)
	}
	// Hand-edit a recorded description: verification must catch the drift.
	raw := readJSON(t, migrated)
	mediaByID(t, raw, "dv")["description"].(map[string]any)["displayWidth"] = float64(720)
	data, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(migrated, data, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Convert(Options{InPath: migrated, OutPath: filepath.Join(dir, "ignored.json"), Prober: prober})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "verify" || rep.Pending() == 0 {
		t.Fatalf("drift not detected: mode=%s pending=%d", rep.Mode, rep.Pending())
	}
	var dv *MediaChange
	for i := range rep.Media {
		if rep.Media[i].MediaID == "dv" {
			dv = &rep.Media[i]
		}
	}
	if dv == nil || dv.Outcome != "drift" {
		t.Errorf("dv outcome = %+v", dv)
	}
}
