package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const latestURL = "https://api.github.com/repos/Leopere/ship-it/releases/latest"

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func Maybe(version string, args []string, out io.Writer) error {
	if version == "dev" || os.Getenv("SHIP_IT_NO_UPDATE") != "" || os.Getenv("SHIP_IT_UPDATED") != "" {
		return nil
	}
	cache, err := os.UserCacheDir()
	if err == nil {
		stamp := filepath.Join(cache, "ship-it", "last-update-check")
		if data, readErr := os.ReadFile(stamp); readErr == nil && strings.TrimSpace(string(data)) == time.Now().UTC().Format("2006-01-02") {
			return nil
		}
		_ = os.MkdirAll(filepath.Dir(stamp), 0o755)
		_ = os.WriteFile(stamp, []byte(time.Now().UTC().Format("2006-01-02")+"\n"), 0o644)
	}
	updated, err := apply(version, out)
	if err != nil {
		fmt.Fprintln(out, "Update check skipped:", err)
		return nil
	}
	if updated {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		env := append(os.Environ(), "SHIP_IT_UPDATED=1")
		return syscall.Exec(exe, append([]string{exe}, args...), env)
	}
	return nil
}

func Force(version string, out io.Writer) error {
	updated, err := apply(version, out)
	if err != nil {
		return err
	}
	if !updated {
		fmt.Fprintln(out, "ship-it is already current.")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "install", "--skills-only")
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func apply(version string, out io.Writer) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 8 * time.Second}
	var rel release
	if err := getJSON(ctx, client, latestURL, &rel); err != nil {
		return false, err
	}
	if rel.TagName == "" || rel.TagName == version {
		return false, nil
	}
	archiveName := fmt.Sprintf("ship-it_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var archiveURL, checksumsURL string
	for _, item := range rel.Assets {
		switch item.Name {
		case archiveName:
			archiveURL = item.URL
		case "checksums.txt":
			checksumsURL = item.URL
		}
	}
	if archiveURL == "" || checksumsURL == "" {
		return false, fmt.Errorf("release %s has no asset for %s/%s", rel.TagName, runtime.GOOS, runtime.GOARCH)
	}
	archive, err := get(ctx, client, archiveURL)
	if err != nil {
		return false, err
	}
	checksums, err := get(ctx, client, checksumsURL)
	if err != nil {
		return false, err
	}
	want := checksumFor(checksums, archiveName)
	if want == "" {
		return false, errors.New("release checksum is missing")
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != want {
		return false, errors.New("release checksum does not match")
	}
	binary, err := extractBinary(archive)
	if err != nil {
		return false, err
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".ship-it-update-*")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(name, exe); err != nil {
		return false, err
	}
	fmt.Fprintf(out, "Updated ship-it from %s to %s.\n", version, rel.TagName)
	return true, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, target any) error {
	data, err := get(ctx, client, url)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ship-it")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func checksumFor(data []byte, name string) string {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return fields[0]
		}
	}
	return ""
}

func extractBinary(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(header.Name) == "ship-it" && header.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
	return nil, errors.New("release archive does not contain ship-it")
}
