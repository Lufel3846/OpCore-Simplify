// Package fetch ports Scripts/resource_fetcher.py: HTTP fetches with retries,
// content decoding (gzip/deflate), parsing (json, plist, text), and downloads
// verified by SHA-256.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package fetch

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/integrity"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// MaxAttempts mirrors MAX_ATTEMPTS in resource_fetcher.py.
const MaxAttempts = 3

// Timeout mirrors the timeout=10 passed to urlopen. Python applies it to each
// socket operation, so it is used here for connection and response headers.
const Timeout = 10 * time.Second

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Fetcher holds the request configuration and the output stream used for the
// same messages the original prints.
type Fetcher struct {
	Headers map[string]string
	Client  *http.Client
	Out     io.Writer
}

// New returns a Fetcher with the original User-Agent and timeouts.
func New(out io.Writer) *Fetcher {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: Timeout}).DialContext,
		ResponseHeaderTimeout: Timeout,
		Proxy:                 http.ProxyFromEnvironment,
		DisableCompression:    true, // Accept-Encoding is set by hand, as in the original
	}
	return &Fetcher{
		Headers: map[string]string{"User-Agent": defaultUserAgent},
		Client:  &http.Client{Transport: transport},
		Out:     out,
	}
}

func (f *Fetcher) printf(format string, args ...any) {
	fmt.Fprintf(f.Out, format+"\n", args...)
}

// makeRequest mirrors ResourceFetcher._make_request. Any failure prints the
// reason and returns nil. Status codes of 400 and above are failures, as
// urlopen raises HTTPError for them.
func (f *Fetcher) makeRequest(url string) *http.Response {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		f.printf("Request failed: %v", err)
		return nil
	}
	for k, v := range f.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := f.Client.Do(req)
	if err != nil {
		var netErr net.Error
		var certErr *tls.CertificateVerificationError
		switch {
		case errors.As(err, &netErr) && netErr.Timeout():
			f.printf("Timeout error: %v", err)
		case errors.As(err, &certErr):
			f.printf("SSL error: %v", err)
		default:
			f.printf("Connection error: %v", err)
		}
		return nil
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		f.printf("Request failed: HTTP Error %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		return nil
	}
	return resp
}

// FetchAndParse mirrors ResourceFetcher.fetch_and_parse_content. contentType is
// "json", "plist", or anything else for decoded text. It returns nil when the
// content cannot be fetched or parsed, as the original does.
func (f *Fetcher) FetchAndParse(url, contentType string) any {
	attempt := 0
	var resp *http.Response
	for attempt < MaxAttempts {
		resp = f.makeRequest(url)
		if resp == nil {
			attempt++
			f.printf("Failed to fetch content from %s. Retrying...", url)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		attempt++
	}
	if resp == nil {
		f.printf("Failed to fetch content from %s", url)
		return nil
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		f.printf("Failed to read content from %s: %v", url, err)
		return nil
	}

	encoding := resp.Header.Get("Content-Encoding")
	if encoding == "gzip" || bytes.HasPrefix(content, []byte{0x1f, 0x8b}) {
		if dec, err := gunzip(content); err != nil {
			f.printf("Failed to decompress gzip content: %v", err)
		} else {
			content = dec
		}
	} else if encoding == "deflate" {
		if dec, err := inflate(content); err != nil {
			f.printf("Failed to decompress deflate content: %v", err)
		} else {
			content = dec
		}
	}

	var parsed any
	switch contentType {
	case "json":
		parsed, err = pyjson.Load(content)
	case "plist":
		parsed, err = plist.Unmarshal(content)
	default:
		if !utf8.Valid(content) {
			err = errors.New("invalid UTF-8")
		} else {
			parsed = string(content)
		}
	}
	if err != nil {
		f.printf("Error parsing content as %s: %v", contentType, err)
		return nil
	}
	return parsed
}

func gunzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func inflate(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// DownloadAndSave mirrors ResourceFetcher.download_and_save_file. It writes the
// body to destination and, when sha256Hash is set, checks the digest and retries
// on mismatch. It returns false after MaxAttempts failed tries.
func (f *Fetcher) DownloadAndSave(url, destination, sha256Hash string) (bool, error) {
	attempt := 0
	for attempt < MaxAttempts {
		attempt++
		resp := f.makeRequest(url)
		if resp == nil {
			f.printf("Failed to fetch content from %s. Retrying...", url)
			continue
		}

		if err := f.writeBody(resp, destination); err != nil {
			resp.Body.Close()
			return false, err
		}
		resp.Body.Close()

		if info, err := os.Stat(destination); err == nil && info.Size() > 0 {
			if sha256Hash == "" {
				f.printf("No SHA256 hash provided. Downloading file without verification.")
				return true, nil
			}
			f.printf("Verifying SHA256 checksum...")
			digest, _, err := integrity.SHA256(destination)
			if err != nil {
				return false, err
			}
			if strings.EqualFold(digest, sha256Hash) {
				f.printf("Checksum verified successfully.")
				return true, nil
			}
			f.printf("Checksum mismatch! Removing file and retrying download...")
			if err := os.Remove(destination); err != nil {
				return false, err
			}
			continue
		}

		if _, err := os.Stat(destination); err == nil {
			if err := os.Remove(destination); err != nil {
				return false, err
			}
		}
		if attempt < MaxAttempts {
			f.printf("Download failed for %s. Retrying...", url)
		}
	}
	f.printf("Failed to download %s after %d attempts.", url, MaxAttempts)
	return false, nil
}

// writeBody copies the response body to destination and prints progress, using
// the same formula as _download_with_progress.
func (f *Fetcher) writeBody(resp *http.Response, destination string) error {
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()

	total := resp.ContentLength
	buf := make([]byte, 16*1024)
	var downloaded int64
	start := time.Now()
	last := start
	var lastBytes int64
	var speeds []float64
	speed := "-- KB/s"

	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := file.Write(buf[:n]); err != nil {
				return err
			}
			downloaded += int64(n)
			now := time.Now()
			if diff := now.Sub(last).Seconds(); diff > 0.5 {
				speeds = append(speeds, float64(downloaded-lastBytes)/diff)
				if len(speeds) > 5 {
					speeds = speeds[1:]
				}
				var sum float64
				for _, s := range speeds {
					sum += s
				}
				avg := sum / float64(len(speeds))
				if avg < 1024*1024 {
					speed = fmt.Sprintf("%.1f KB/s", avg/1024)
				} else {
					speed = fmt.Sprintf("%.1f MB/s", avg/(1024*1024))
				}
				last = now
				lastBytes = downloaded
			}
			f.printProgress(speed, downloaded, total)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	fmt.Fprintln(f.Out)
	return nil
}

func (f *Fetcher) printProgress(speed string, downloaded, total int64) {
	mb := float64(downloaded) / (1024 * 1024)
	if total > 0 {
		percent := int(float64(downloaded) / float64(total) * 100)
		const barLength = 40
		filled := int(float64(barLength) * float64(downloaded) / float64(total))
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barLength-filled)
		fmt.Fprintf(f.Out, "%s [%s] %3d%% %.1f/%.1fMB", speed, bar, percent, mb, float64(total)/(1024*1024))
	} else {
		fmt.Fprintf(f.Out, "%s %.1fMB downloaded", speed, mb)
	}
	fmt.Fprint(f.Out, "\r")
}
