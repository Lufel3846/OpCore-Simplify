// Package github ports Scripts/github.py: it reads GitHub release pages and
// extracts the release tag, body and downloadable assets from their markup.
//
// The original scrapes github.com HTML, and so does this port. The parsers
// are pure functions and are compared with the original in the tests.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package github

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fetch"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// ExtractPayload mirrors Github.extract_payload: it returns the "payload" of the
// first line carrying a JSON island (type="application/json"), or nil.
func ExtractPayload(response string) (any, error) {
	for _, line := range splitLines(response) {
		if !strings.Contains(line, `type="application/json"`) {
			continue
		}
		_, rest, ok := strings.Cut(line, ">")
		if !ok {
			return nil, errors.New("list index out of range")
		}
		payloadText, _, _ := strings.Cut(rest, "<")
		decoded, err := pyjson.Load([]byte(payloadText))
		if err != nil {
			continue
		}
		obj, ok := decoded.(*plist.Dict)
		if !ok {
			continue
		}
		payload, ok := obj.Get("payload")
		if !ok {
			continue
		}
		return payload, nil
	}
	return nil, nil
}

// ExtractTagName mirrors Github._extract_tag_name. It returns "" when no
// release tag link is found (Python returns None).
func ExtractTagName(response string) string {
	for _, line := range splitLines(response) {
		if strings.Contains(line, "<a") && strings.Contains(line, `href="`) && strings.Contains(line, "/releases/tag/") {
			_, after, _ := strings.Cut(line, "/releases/tag/")
			tag, _, _ := strings.Cut(after, `"`)
			return tag
		}
	}
	return ""
}

// ExtractBody mirrors Github._extract_body_content. It returns an error where
// Python would raise IndexError (the marker line is not in the response).
func ExtractBody(response string) (string, error) {
	for _, line := range splitLines(response) {
		if strings.Contains(line, "<div") && strings.Contains(line, "body-content") {
			head, _, _ := strings.Cut(line, ">")
			parts := strings.SplitN(response, head, 2)
			if len(parts) < 2 {
				return "", errors.New("list index out of range")
			}
			body, _, _ := strings.Cut(parts[1], "</div>")
			return dropFirstRune(body), nil
		}
	}
	return "", nil
}

// dropFirstRune mirrors Python's [1:] on the extracted body.
func dropFirstRune(s string) string {
	for i := range s {
		if i == 0 {
			continue
		}
		return s[i:]
	}
	return ""
}

// ExtractAssets mirrors Github._extract_assets. Each asset is a *plist.Dict with
// keys product_name, id, url and sha256 (nil when no hash was found), in that
// order.
func ExtractAssets(response string) ([]*plist.Dict, error) {
	var assets []*plist.Dict
	inLi := false
	var downloadLink, sha256, assetID *string

	for _, line := range splitLines(response) {
		if strings.Contains(line, "<li") {
			inLi = true
			downloadLink, sha256, assetID = nil, nil, nil
		} else if inLi && strings.Contains(line, "</li") {
			if downloadLink != nil && *downloadLink != "" && assetID != nil && *assetID != "" {
				parts := strings.Split(*downloadLink, "/")
				id, err := strconv.ParseInt(*assetID, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid literal for int(): %q", *assetID)
				}
				asset := plist.NewDict()
				asset.Set("product_name", ExtractAssetName(parts[len(parts)-1]))
				asset.Set("id", id)
				asset.Set("url", "https://github.com"+*downloadLink)
				if sha256 != nil {
					asset.Set("sha256", *sha256)
				} else {
					asset.Set("sha256", nil)
				}
				assets = append(assets, asset)
			}
			inLi = false
		}

		if !inLi {
			continue
		}
		if downloadLink == nil && strings.Contains(line, "<a") && strings.Contains(line, `href="`) && strings.Contains(line, "/releases/download") {
			_, after, _ := strings.Cut(line, `href="`)
			link, _, _ := strings.Cut(after, `"`)
			downloadLink = &link
			keep := strings.Contains(link, "tlwm") ||
				(!strings.Contains(link, "tlwm") && !strings.Contains(strings.ToUpper(link), "RESEARCH") && !strings.Contains(strings.ToUpper(link), "DEBUG"))
			if !keep {
				inLi = false
				continue
			}
		}
		if sha256 == nil && strings.Contains(line, "sha256:") {
			_, after, _ := strings.Cut(line, "sha256:")
			value, _, _ := strings.Cut(after, "<")
			sha256 = &value
		}
		if assetID == nil && strings.Contains(line, "<relative-time") {
			id := generateAssetID(line)
			assetID = &id
		}
	}
	return assets, nil
}

// generateAssetID mirrors Github._generate_asset_id: the digits of the
// datetime attribute, read in reverse, first nine of them.
func generateAssetID(line string) string {
	// Python takes split('datetime="')[-1], the text after the last occurrence.
	after := line
	if idx := strings.LastIndex(line, `datetime="`); idx >= 0 {
		after = line[idx+len(`datetime="`):]
	}
	value, _, _ := strings.Cut(after, `"`)
	runes := []rune(value)
	var digits []rune
	for i := len(runes) - 1; i >= 0; i-- {
		if unicode.IsDigit(runes[i]) {
			digits = append(digits, runes[i])
		}
	}
	if len(digits) > 9 {
		digits = digits[:9]
	}
	return string(digits)
}

// ExtractAssetName mirrors Github.extract_asset_name. Indices are computed over
// runes, like Python's str indices.
func ExtractAssetName(fileName string) string {
	runes := []rune(fileName)
	endIdx := len(runes)
	if i := indexRune(runes, '-'); i >= 0 && i < endIdx {
		endIdx = i
	}
	if i := indexRune(runes, '_'); i >= 0 && i < endIdx {
		endIdx = i
	}
	if i := indexRune(runes, '.'); i >= 0 {
		if i < endIdx {
			endIdx = i
		}
		// Python: file_name[end_idx] == "." and file_name[end_idx - 1].isdigit()
		prev := endIdx - 1
		if prev < 0 {
			prev += len(runes)
		}
		if endIdx < len(runes) && runes[endIdx] == '.' && prev >= 0 && prev < len(runes) && unicode.IsDigit(runes[prev]) {
			endIdx--
		}
	}
	assetName := string(runes[:endIdx])

	if strings.Contains(fileName, "Sniffer") {
		assetName = strings.SplitN(fileName, ".", 2)[0]
	}
	if assetName == "IntelBluetooth" {
		assetName = "IntelBluetoothFirmware"
	}
	if strings.Contains(fileName, "unsupported") {
		assetName += "-unsupported"
	} else if strings.Contains(fileName, "rtsx") {
		assetName += "-rtsx"
	} else if strings.Contains(strings.ToLower(fileName), "itlwm") {
		switch {
		case strings.Contains(fileName, "Sequoia"):
			assetName += "24"
		case strings.Contains(fileName, "Tahoe"):
			assetName += "25"
		case strings.Contains(fileName, "Sonoma14.4"):
			assetName += "23.4"
		case strings.Contains(fileName, "Sonoma14.0"):
			assetName += "23.0"
		case strings.Contains(fileName, "Ventura"):
			assetName += "22"
		case strings.Contains(fileName, "Monterey"):
			assetName += "21"
		case strings.Contains(fileName, "Big_Sur"):
			assetName += "20"
		case strings.Contains(fileName, "Catalina"):
			assetName += "19"
		case strings.Contains(fileName, "Mojave"):
			assetName += "18"
		case strings.Contains(fileName, "High_Sierra"):
			assetName += "17"
		}
	}
	return assetName
}

func indexRune(runes []rune, r rune) int {
	for i, c := range runes {
		if c == r {
			return i
		}
	}
	return -1
}

// splitLines mirrors str.splitlines for the separators that appear in HTML
// (\n, \r\n and \r).
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Release is the result of GetLatestRelease.
type Release struct {
	Body   string
	Assets []*plist.Dict
}

// GetLatestRelease mirrors Github.get_latest_release. It fetches the releases
// page and the expanded assets of the tag it finds.
func GetLatestRelease(f *fetch.Fetcher, owner, repo string) (*Release, error) {
	url := fmt.Sprintf("https://github.com/%s/%s/releases", owner, repo)
	response, ok := f.FetchAndParse(url, "").(string)
	if !ok || response == "" {
		return nil, errors.New("Failed to fetch release information from GitHub.")
	}
	tagName := ExtractTagName(response)
	body, err := ExtractBody(response)
	if err != nil {
		return nil, err
	}

	// Python formats a missing tag as the text "None".
	if tagName == "" {
		tagName = "None"
	}
	tagURL := fmt.Sprintf("https://github.com/%s/%s/releases/expanded_assets/%s", owner, repo, tagName)
	response, ok = f.FetchAndParse(tagURL, "").(string)
	if !ok || response == "" {
		return nil, errors.New("Failed to fetch expanded assets information from GitHub.")
	}
	assets, err := ExtractAssets(response)
	if err != nil {
		return nil, err
	}
	return &Release{Body: body, Assets: assets}, nil
}
