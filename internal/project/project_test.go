package project

import (
	"encoding/json"
	"strings"
	"testing"

	"oralcut/internal/media"
)

func descFixture() *media.Description {
	return &media.Description{
		Version: media.DescriptionVersion,
		StoredWidth: 720, StoredHeight: 576,
		Rotation: 0,
		SARNum: 64, SARDen: 45,
		DisplayWidth: 1024, DisplayHeight: 576,
		Duration: 6, HasAudio: true,
	}
}

func TestDisplayDimsPrefersDescription(t *testing.T) {
	m := &Media{Width: 720, Height: 576, Description: descFixture()}
	w, h := m.DisplayDims()
	if w != 1024 || h != 576 {
		t.Errorf("DisplayDims = %dx%d, want 1024x576", w, h)
	}
	// Legacy media without a description falls back to flat fields.
	legacy := &Media{Width: 720, Height: 1280}
	w, h = legacy.DisplayDims()
	if w != 720 || h != 1280 {
		t.Errorf("legacy DisplayDims = %dx%d, want 720x1280", w, h)
	}
	// A broken description (zero display) must not win.
	broken := &Media{Width: 640, Height: 480, Description: &media.Description{}}
	if w, h := broken.DisplayDims(); w != 640 || h != 480 {
		t.Errorf("broken description DisplayDims = %dx%d", w, h)
	}
}

func TestNormalizeForSaveMirrorsDescription(t *testing.T) {
	p := New("t", "t")
	p.FormatVersion = 0 // simulate a legacy project being migrated
	p.Media = []Media{
		{ID: "dv", Width: 720, Height: 576, Duration: 6, Description: descFixture()},
		{ID: "lost", Width: 1920, Height: 1080, Duration: 10}, // no description
	}
	p.NormalizeForSave()
	m := p.Media[0]
	if m.Width != 1024 || m.Height != 576 {
		t.Errorf("mirror = %dx%d, want 1024x576", m.Width, m.Height)
	}
	if !m.HasAudio || m.Duration != 6 {
		t.Error("audio/duration not mirrored")
	}
	if p.FormatVersion != FormatVersionCurrent {
		t.Errorf("formatVersion = %d, want %d", p.FormatVersion, FormatVersionCurrent)
	}
	// Media without a description keeps its legacy fields untouched.
	if p.Media[1].Width != 1920 {
		t.Error("description-less media must not be rewritten")
	}
}

func TestLegacyEnvelopeRoundTrip(t *testing.T) {
	p := New("t", "t")
	p.Legacy = json.RawMessage(`{"fromFormatVersion":1,"unknownProjectFields":{"archiveShelfmark":"架A-07"}}`)
	dir := t.TempDir()
	if err := p.Save(dir); err != nil {
		t.Fatal(err)
	}
	q, err := Load(dir, "t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(q.Legacy), "archiveShelfmark") {
		t.Error("legacy envelope lost in save/load round trip")
	}
	if q.FormatVersion != FormatVersionCurrent {
		t.Errorf("formatVersion = %d", q.FormatVersion)
	}
}

func TestValidateRejectsInconsistentDescription(t *testing.T) {
	p := New("t", "t")
	d := descFixture()
	d.DisplayWidth = 720 // contradicts stored 720x576 @ 64:45 (would be 1024)
	p.Media = []Media{{ID: "dv", Width: 1024, Height: 576, Duration: 6,
		Status: StatusOK, Description: d}}
	p.Clips = []Clip{{ID: "c1", MediaID: "dv", Kind: ClipVideo, Label: "dv",
		In: 0, Out: 2, Fit: "full"}}
	issues := p.Validate(false)
	found := false
	for _, i := range issues {
		if strings.Contains(i.Message, "描述内部不一致") {
			found = true
		}
	}
	if !found {
		t.Errorf("inconsistent description not flagged: %v", issues)
	}
}
