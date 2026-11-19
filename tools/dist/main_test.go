package main

import "testing"

func TestChangelogSection(t *testing.T) {
	doc := "# Changelog\n\n## Unreleased\n\n- next\n\n## v0.4.5 - 2026-11-18\n\n### Fixes\n- a\n- b\n\n## v0.4.4\n- old\n"
	if got := changelogSection(doc, "v0.4.5"); got != "### Fixes\n- a\n- b" {
		t.Fatalf("v0.4.5: %q", got)
	}
	if got := changelogSection(doc, "Unreleased"); got != "- next" {
		t.Fatalf("Unreleased: %q", got)
	}
	if got := changelogSection(doc, "v0.4.4"); got != "- old" {
		t.Fatalf("last section: %q", got)
	}
	if got := changelogSection(doc, "v0.4"); got != "" {
		t.Fatalf("prefix matched: %q", got)
	}
}

func TestReleaseTag(t *testing.T) {
	for v, want := range map[string]bool{"v0.4.5": true, "v0.5.0-rc.1": true, "v0.4.5-3-g19bb261": false, "v0.4.5-dirty": false, "dev": false, "19bb261": false} {
		if got := releaseTag(v); got != want {
			t.Errorf("%s: %v", v, got)
		}
	}
}
