package safe

import "testing"

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"Der Name des Windes":             "Der Name des Windes",
		"Évian · Größe – „Zitat“ 日本語":     "Évian · Größe – „Zitat“ 日本語",
		"Evil\x1b]52;c;aGFjaw==\x07Title": "Evil]52;c;aGFjaw==Title",
		"red\x1b[31mtext\x1b[0m":          "red[31mtext[0m",
		"C1\u009b31mcontrol":              "C131mcontrol",
		"bidi ‮exe.txt‬":                  "bidi exe.txt",
		"two\nlines\ttab\rcr":             "twolinestabcr",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Multiline("a\nb\x1b[2Jc"); got != "a\nb[2Jc" {
		t.Errorf("Multiline: %q", got)
	}
}
