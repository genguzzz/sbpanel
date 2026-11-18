package logx

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewEscapesLineBreaks(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "[t] ")
	l.Printf("profile %s: %v", "evil\n[t] 2026/01/01 00:00:00 forged", "err\r\nmore")
	l.Print("plain")
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], `evil\n[t] 2026/01/01 00:00:00 forged`) || !strings.Contains(lines[0], `err\r\nmore`) {
		t.Fatalf("not escaped: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "plain") {
		t.Fatalf("plain line: %q", lines[1])
	}
}
