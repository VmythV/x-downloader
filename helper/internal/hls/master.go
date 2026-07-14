package hls

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	mediaIDPattern = regexp.MustCompile(`/(?:amplify_video|ext_tw_video)/(\d+)(?:/|$)`)
	bitratePattern = regexp.MustCompile(`/pl/mp4a/(\d+)/`)
)

type Audio struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name,omitempty"`
	URL     string `json:"url"`
	Bitrate int    `json:"bitrate,omitempty"`
}

type Variant struct {
	ID               string `json:"id"`
	URL              string `json:"url"`
	Width            int    `json:"width,omitempty"`
	Height           int    `json:"height,omitempty"`
	Bandwidth        int    `json:"bandwidth,omitempty"`
	AverageBandwidth int    `json:"averageBandwidth,omitempty"`
	Codecs           string `json:"codecs,omitempty"`
	AudioGroup       string `json:"audioGroup,omitempty"`
	Audio            *Audio `json:"audio,omitempty"`
}

type Master struct {
	URL      string    `json:"url"`
	MediaID  string    `json:"mediaId,omitempty"`
	Variants []Variant `json:"variants"`
	Audios   []Audio   `json:"audios"`
}

func ParseMaster(text, masterURL string) (Master, error) {
	base, err := ValidatePlaylistURL(masterURL)
	if err != nil {
		return Master{}, err
	}
	lines := splitLines(text)
	audioByGroup := make(map[string]Audio)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#EXT-X-MEDIA:") {
			continue
		}
		attributes := ParseAttributeList(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
		if attributes["TYPE"] != "AUDIO" || attributes["GROUP-ID"] == "" || attributes["URI"] == "" {
			continue
		}
		resolved, err := resolvePlaylistURL(base, attributes["URI"])
		if err != nil {
			return Master{}, fmt.Errorf("resolve audio rendition: %w", err)
		}
		audio := Audio{
			GroupID: attributes["GROUP-ID"],
			Name:    attributes["NAME"],
			URL:     resolved.String(),
			Bitrate: bitrateFromURL(resolved),
		}
		audioByGroup[audio.GroupID] = audio
	}

	variants := make([]Variant, 0)
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			continue
		}
		attributes := ParseAttributeList(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
		uri := ""
		for index++; index < len(lines); index++ {
			candidate := strings.TrimSpace(lines[index])
			if candidate != "" && !strings.HasPrefix(candidate, "#") {
				uri = candidate
				break
			}
		}
		if uri == "" {
			return Master{}, errors.New("variant is missing its playlist URI")
		}
		resolved, err := resolvePlaylistURL(base, uri)
		if err != nil {
			return Master{}, fmt.Errorf("resolve video variant: %w", err)
		}
		width, height := parseResolution(attributes["RESOLUTION"])
		variant := Variant{
			URL:              resolved.String(),
			Width:            width,
			Height:           height,
			Bandwidth:        positiveInt(attributes["BANDWIDTH"]),
			AverageBandwidth: positiveInt(attributes["AVERAGE-BANDWIDTH"]),
			Codecs:           attributes["CODECS"],
			AudioGroup:       attributes["AUDIO"],
		}
		variant.ID = variantID(variant)
		if audio, ok := audioByGroup[variant.AudioGroup]; ok {
			copy := audio
			variant.Audio = &copy
		}
		variants = append(variants, variant)
	}

	if len(variants) == 0 {
		return Master{}, errors.New("playlist does not contain video variants")
	}
	sort.SliceStable(variants, func(i, j int) bool {
		leftPixels := variants[i].Width * variants[i].Height
		rightPixels := variants[j].Width * variants[j].Height
		if leftPixels != rightPixels {
			return leftPixels > rightPixels
		}
		leftBandwidth := variants[i].AverageBandwidth
		if leftBandwidth == 0 {
			leftBandwidth = variants[i].Bandwidth
		}
		rightBandwidth := variants[j].AverageBandwidth
		if rightBandwidth == 0 {
			rightBandwidth = variants[j].Bandwidth
		}
		return leftBandwidth > rightBandwidth
	})

	audios := make([]Audio, 0, len(audioByGroup))
	for _, audio := range audioByGroup {
		audios = append(audios, audio)
	}
	sort.Slice(audios, func(i, j int) bool { return audios[i].Bitrate > audios[j].Bitrate })

	return Master{
		URL:      base.String(),
		MediaID:  MediaID(base),
		Variants: variants,
		Audios:   audios,
	}, nil
}

func ValidatePlaylistURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("parse playlist URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() != "video.twimg.com" || parsed.Port() != "" {
		return nil, errors.New("playlist URL must use https://video.twimg.com")
	}
	if parsed.User != nil || !strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		return nil, errors.New("playlist URL must be an m3u8 without user information")
	}
	return parsed, nil
}

func MediaID(value *url.URL) string {
	match := mediaIDPattern.FindStringSubmatch(value.Path)
	if match == nil {
		return ""
	}
	return match[1]
}

func ParseAttributeList(source string) map[string]string {
	result := make(map[string]string)
	for index := 0; index < len(source); {
		keyStart := index
		for index < len(source) && source[index] != '=' {
			index++
		}
		if index >= len(source) {
			break
		}
		key := strings.ToUpper(strings.TrimSpace(source[keyStart:index]))
		index++

		value := ""
		if index < len(source) && source[index] == '"' {
			index++
			var builder strings.Builder
			for index < len(source) {
				if source[index] == '\\' && index+1 < len(source) {
					builder.WriteByte(source[index+1])
					index += 2
					continue
				}
				if source[index] == '"' {
					index++
					break
				}
				builder.WriteByte(source[index])
				index++
			}
			value = builder.String()
		} else {
			valueStart := index
			for index < len(source) && source[index] != ',' {
				index++
			}
			value = strings.TrimSpace(source[valueStart:index])
		}
		if key != "" {
			result[key] = value
		}
		for index < len(source) && (source[index] == ',' || source[index] == ' ') {
			index++
		}
	}
	return result
}

func resolvePlaylistURL(base *url.URL, reference string) (*url.URL, error) {
	relative, err := url.Parse(reference)
	if err != nil {
		return nil, err
	}
	resolved := base.ResolveReference(relative)
	return ValidatePlaylistURL(resolved.String())
}

func splitLines(text string) []string {
	lines := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func positiveInt(value string) int {
	number, _ := strconv.Atoi(value)
	if number < 0 {
		return 0
	}
	return number
}

func parseResolution(value string) (int, int) {
	parts := strings.SplitN(strings.ToLower(value), "x", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return positiveInt(parts[0]), positiveInt(parts[1])
}

func bitrateFromURL(value *url.URL) int {
	match := bitratePattern.FindStringSubmatch(value.Path)
	if match == nil {
		return 0
	}
	return positiveInt(match[1])
}

func variantID(variant Variant) string {
	bandwidth := variant.AverageBandwidth
	if bandwidth == 0 {
		bandwidth = variant.Bandwidth
	}
	digest := sha256.Sum256([]byte(variant.URL + "|" + variant.AudioGroup))
	return fmt.Sprintf("%dx%d-%d-%s", variant.Width, variant.Height, bandwidth, hex.EncodeToString(digest[:4]))
}
