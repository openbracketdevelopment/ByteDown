// Package filename defines the process-to-worker output filename contract.
package filename

import (
	"errors"
	"fmt"
)

func For(format string, width int) (string, error) {
	switch format {
	case "jpeg", "png", "webp", "avif":
	default:
		return "", errors.New("invalid output format")
	}
	if width < 1 {
		return "", errors.New("invalid output width")
	}
	extension := format
	if format == "jpeg" {
		extension = "jpg"
	}
	return fmt.Sprintf("%s-%d.%s", format, width, extension), nil
}
