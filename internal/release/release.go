package release

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Release struct {
	Build            string    `json:"build"`
	Version          string    `json:"version"`
	MinimumSystem    string    `json:"minimum_system,omitempty"`
	Architecture     string    `json:"architecture,omitempty"`
	URL              string    `json:"url"`
	Length           int64     `json:"length,omitempty"`
	SparkleSignature string    `json:"sparkle_signature,omitempty"`
	PublicationTime  time.Time `json:"publication_time"`
}

func (r Release) Validate() error {
	if !IsNumericVersion(r.Build) {
		return fmt.Errorf("invalid release build %q", r.Build)
	}
	parsed, err := url.Parse(r.URL)
	if err != nil || parsed.Scheme != "https" && (parsed.Scheme != "http" || !isLoopback(parsed.Hostname())) {
		return fmt.Errorf("invalid release URL %q", r.URL)
	}
	if r.Length < 0 {
		return fmt.Errorf("invalid release length %d", r.Length)
	}
	return nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func Compare(left, right string) (int, error) {
	if !IsNumericVersion(left) || !IsNumericVersion(right) {
		return 0, fmt.Errorf("compare invalid numeric versions %q and %q", left, right)
	}
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	length := max(len(leftParts), len(rightParts))
	for index := range length {
		var leftValue, rightValue uint64
		var err error
		if index < len(leftParts) {
			leftValue, err = strconv.ParseUint(leftParts[index], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse version %q: %w", left, err)
			}
		}
		if index < len(rightParts) {
			rightValue, err = strconv.ParseUint(rightParts[index], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse version %q: %w", right, err)
			}
		}
		if leftValue < rightValue {
			return -1, nil
		}
		if leftValue > rightValue {
			return 1, nil
		}
	}
	return 0, nil
}

func IsNumericVersion(value string) bool {
	if strings.TrimSpace(value) != value || value == "" {
		return false
	}
	for part := range strings.SplitSeq(value, ".") {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

func Key(version string) string {
	return strings.ReplaceAll(version, ".", "_")
}
