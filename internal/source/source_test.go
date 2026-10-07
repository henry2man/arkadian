package source

import "testing"

func TestSourceAndDownloadSize(test *testing.T) {
	if repo, err := Parse("hf://org/model"); err != nil || repo != "org/model" {
		test.Fatalf("source: %s %v", repo, err)
	}
	for _, uri := range []string{"org/model", "modelscope://org/model", "hf://../outside"} {
		if _, err := Parse(uri); err == nil {
			test.Fatalf("accepted %s", uri)
		}
	}
	for output, expected := range map[string]int64{
		"[dry-run] Will download 2 files totalling 5.6G.": 6600000000,
		"[dry-run] Will download 0 files totalling 0.":    0,
		"[dry-run] Will download 1 files totalling 12K.":  13000,
	} {
		if size, err := ParseDownloadBytes(output); err != nil || size != expected {
			test.Fatalf("size %d, want %d: %v", size, expected, err)
		}
	}
	if _, err := ParseDownloadBytes("unknown output"); err == nil {
		test.Fatal("unknown dry-run format accepted")
	}
}
