// Package filename defines the process-to-worker output filename contract.
package filename

import (
	"errors"
	"fmt"
)

func For(format string, width int) (string, error) {
	switch format {
	case "jpeg", "png", "webp", "avif", "gif":
	default:
		return "", errors.New("invalid output format")
	}
	if width < 1 {
		return "", errors.New("invalid output width")
	}
	return fmt.Sprintf("%s-%d.%s", format, width, format), nil
}
