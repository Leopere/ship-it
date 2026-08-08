package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestChecksumAndExtractBinary(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	payload := []byte("binary")
	if err := tw.WriteHeader(&tar.Header{Name: "ship-it_Darwin_arm64/ship-it", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := extractBinary(archive.Bytes())
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("got=%q err=%v", got, err)
	}
	sum := sha256.Sum256(archive.Bytes())
	checksums := []byte(hex.EncodeToString(sum[:]) + "  ship-it_Darwin_arm64.tar.gz\n")
	if got := checksumFor(checksums, "ship-it_Darwin_arm64.tar.gz"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum=%q", got)
	}
}
