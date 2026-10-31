package config

import (
	"fmt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/term"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// recommendedAuthors are the layout authors preferred by the original.
var recommendedAuthors = []string{"Mirone", "InsanelyDeepak", "Toleda", "DalianSky"}

// selectAudioCodecLayout mirrors Prodigy.select_audio_codec_layout. It returns
// the chosen layout ID (int64, or nil) and the audio controller entry (or nil).
//
// The first block is a bare try/except in the original: any error while reading
// config stops that block silently and the function carries on.
func (p *Prodigy) selectAudioCodecLayout(report any, config any, controllerRequired bool) (any, any, error) {
	if config != nil {
		if adds, err := index(config, "DeviceProperties", "Add"); err == nil {
			if values, err := pyrt.Values(adds); err == nil {
				for _, props := range values {
					flag, err := pyrt.Get(props, "layout-id")
					if err != nil {
						break
					}
					if pyrt.Truthy(flag) {
						return nil, nil, nil
					}
				}
			}
		}
	}

	var codecID any
	var controller any
	soundObj, err := pyrt.GetDefault(report, "Sound", plist.NewDict())
	if err != nil {
		return nil, nil, err
	}
	sounds, err := pyrt.Values(soundObj)
	if err != nil {
		return nil, nil, err
	}
	for _, codec := range sounds {
		deviceID, err := pyrt.Get(codec, "Device ID")
		if err != nil {
			return nil, nil, err
		}
		if !codecLayoutHas(deviceID) {
			continue
		}
		codecID = deviceID
		ctrlID, err := pyrt.Get(codec, "Controller Device ID")
		if err != nil {
			return nil, nil, err
		}
		if pyrt.Truthy(ctrlID) {
			sysObj, err := pyrt.Get(report, "System Devices")
			if err != nil {
				return nil, nil, err
			}
			devices, err := pyrt.Items(sysObj)
			if err != nil {
				return nil, nil, err
			}
			for _, dev := range devices {
				devID, err := pyrt.Get(dev.Value, "Device ID")
				if err != nil {
					return nil, nil, err
				}
				if pyrt.Equal(devID, ctrlID) {
					controller = dev.Value
					break
				}
			}
		}
		break
	}

	if !pyrt.Truthy(codecID) {
		return nil, nil, nil
	}
	if controllerRequired && !pyrt.Truthy(controller) {
		return nil, nil, nil
	}

	codecKey, ok := codecID.(string)
	if !ok {
		return nil, nil, fmt.Errorf("TypeError: codec ID %v is not a string", codecID)
	}
	available := datasets.CodecLayouts[codecKey]

	var recommended []datasets.CodecLayout
	for _, layout := range available {
		if containsAnyAuthor(recommendedAuthors, layout.Comment) != "" {
			recommended = append(recommended, layout)
		}
	}
	candidates := recommended
	if len(candidates) == 0 {
		candidates = available
	}
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("IndexError: cannot choose from an empty sequence")
	}
	defaultLayout := candidates[p.Choose(len(candidates))]

	for {
		if p.Log != nil {
			contents := []string{"", "List of Codec Layouts:", "", "ID   Comment", strings.Repeat("-", 66)}
			for _, layout := range available {
				comment := []rune(layout.Comment)
				if len(comment) > 60 {
					comment = comment[:60]
				}
				line := fmt.Sprintf("%-4d %s", layout.ID, string(comment))
				if layout == defaultLayout {
					line = "\033[1;32m" + line + "\033[0m"
				}
				contents = append(contents, line)
			}
			contents = append(contents, "", "\033[1;93mNote:\033[0m",
				"- The default layout may not be optimal.",
				"- Test different layouts to find what works best for your system.", "")
			content := strings.Join(contents, "\n")
			term.AdjustWindowSize(p.Log, content)
			term.Head(p.Log, "Choosing Codec Layout ID", 68, false)
			fmt.Fprintln(p.Log, content)
		}
		answer, err := p.ask(fmt.Sprintf("Enter the ID of the codec layout you want to use (default: %d): ", defaultLayout.ID))
		if err != nil {
			return nil, nil, err
		}
		selected := int64(defaultLayout.ID)
		if answer != "" {
			value, err := pyrt.Int(answer)
			if err != nil {
				continue
			}
			n, err := pyrt.IntValue(value)
			if err != nil {
				continue
			}
			selected = n
		}
		for _, layout := range available {
			if int64(layout.ID) == selected {
				return selected, controller, nil
			}
		}
	}
}

// codecLayoutHas mirrors "Device ID in codec_layouts.data".
func codecLayoutHas(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	_, found := datasets.CodecLayouts[s]
	return found
}
