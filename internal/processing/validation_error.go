package processing

import (
	"strings"

	"github.com/openbracketdevelopment/ByteDown/internal/model"
)

// PublicValidationError maps diagnostics to fixed, actionable messages. Never
// forward a native error string: it can contain paths or embedded image metadata.
func PublicValidationError(err error) *model.ValidationError {
	text := strings.ToLower(err.Error())
	code, message := "invalid_image", "The image could not be decoded completely. Export it again as a static JPG or PNG and retry."
	switch {
	case strings.Contains(text, "dimension") || strings.Contains(text, "pixel"):
		code, message = "image_dimensions", "This image exceeds the configured pixel or dimension limit. Resize it and retry."
	case strings.Contains(text, "only still"):
		code, message = "unsupported_format", "The file contents are not a supported JPG or PNG image. Renaming the extension does not convert it."
	case strings.Contains(text, "animation") || strings.Contains(text, "animated"):
		code, message = "animated_image", "Animated PNG images are not supported. Export a single frame as a static PNG."
	case strings.Contains(text, "16-bit") || strings.Contains(text, "non-8-bit") || strings.Contains(text, "hdr png"):
		code, message = "unsupported_precision", "This image uses unsupported high-bit-depth or HDR data. Export an 8-bit sRGB JPG or PNG."
	case strings.Contains(text, "icc") || strings.Contains(text, "colorspace") || strings.Contains(text, "color configuration"):
		code, message = "unsupported_color_profile", "This image has an unsupported or invalid color profile. Convert it to sRGB and export it again."
	case strings.Contains(text, "multiple images") || strings.Contains(text, "multi-picture"):
		code, message = "unsupported_jpeg_structure", "This JPEG contains extra data or multiple pictures that ByteDown cannot safely interpret. Export a standard single-image JPG and retry."
	}
	return &model.ValidationError{Code: code, Message: message}
}
