package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestAssetName(t *testing.T) {
	tests := []struct {
		version, goos, goarch, want string
	}{
		{"0.3.0", "darwin", "arm64", "multibird_0.3.0_darwin_arm64.tar.gz"},
		{"0.3.0", "linux", "amd64", "multibird_0.3.0_linux_amd64.tar.gz"},
	}
	for _, tt := range tests {
		if got := AssetName(tt.version, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("AssetName(%s,%s,%s) = %q, want %q", tt.version, tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	txt := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  multibird_0.3.0_linux_amd64.tar.gz\n" +
		"malformed line\n" +
		"ABCDEF6789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  multibird_0.3.0_darwin_arm64.tar.gz\n"
	sums := ParseChecksums(txt)
	if len(sums) != 2 {
		t.Fatalf("parsed %d entries, want 2: %v", len(sums), sums)
	}
	if sums["multibird_0.3.0_darwin_arm64.tar.gz"] != "abcdef6789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Errorf("digest not lowercased: %v", sums)
	}
}

func TestExtractBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{
		"README.md":              "docs",
		"multibird_v1/multibird": "BINARY-CONTENT",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractBinary(buf.Bytes(), "multibird")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "BINARY-CONTENT" {
		t.Errorf("extracted %q", got)
	}

	if _, err := extractBinary(buf.Bytes(), "nope"); err == nil {
		t.Error("expected error for missing binary")
	}
}
