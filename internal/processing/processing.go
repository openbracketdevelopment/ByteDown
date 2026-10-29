package processing

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
)

// Capability describes a format supported by the actual installed codecs.
type Capability struct {
	Format    string `json:"format"`
	Input     bool   `json:"input"`
	Output    bool   `json:"output"`
	Available bool   `json:"available"`
}

var (
	startOnce    sync.Once
	startErr     error
	probeOnce    sync.Once
	probeErr     error
	capabilities []Capability
)

func startup() error {
	startOnce.Do(func() {
		vips.LoggingSettings(nil, vips.LogLevelWarning)
		startErr = vips.Startup(&vips.Config{
			ConcurrencyLevel: 1, MaxCacheMem: 1, MaxCacheSize: 0, MaxCacheFiles: 0,
		})
	})
	return startErr
}

func limitsWithDefaults(l model.Limits) (model.Limits, error) {
	if l.UploadBytes < 0 || l.Pixels < 0 || l.Dimension < 0 ||
		l.OutputCount < 0 || l.OutputBytes < 0 || l.Duration < 0 {
		return l, errors.New("negative processing limit")
	}
	if l.UploadBytes == 0 {
		l.UploadBytes = 20 << 20
	}
	if l.Pixels == 0 {
		l.Pixels = 40_000_000
	}
	if l.Dimension == 0 {
		l.Dimension = 20_000
	}
	if l.OutputCount == 0 {
		l.OutputCount = 16
	}
	if l.OutputBytes == 0 {
		l.OutputBytes = 100 << 20
	}
	return l, nil
}

func boundedDimensions(w, h int, l model.Limits) error {
	if w <= 0 || h <= 0 || w > l.Dimension || h > l.Dimension ||
		int64(w) > l.Pixels/int64(h) || int64(w) > (1<<31-1)/8/int64(h) {
		return errors.New("image dimensions exceed limits")
	}
	return nil
}

func readInput(path string, l model.Limits) ([]byte, model.Input, error) {
	var input model.Input
	f, err := os.Open(path)
	if err != nil {
		return nil, input, fmt.Errorf("open input: %w", err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, input, err
	}
	if !stat.Mode().IsRegular() || stat.Size() <= 0 || stat.Size() > l.UploadBytes {
		return nil, input, errors.New("input size exceeds limits or is not a regular file")
	}
	// Stat alone is insufficient if a file grows after it was opened.
	reader := &io.LimitedReader{R: f, N: l.UploadBytes}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, input, err
	}
	var extra [1]byte
	n, e := f.Read(extra[:])
	if n != 0 || (e != nil && e != io.EOF) {
		return nil, input, errors.New("input exceeds upload limit")
	}
	if bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		input.Format = "png"
	} else if bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}) {
		input.Format = "jpeg"
	} else {
		return nil, input, errors.New("only still JPEG and PNG inputs are supported")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != input.Format {
		return nil, input, errors.New("invalid image header")
	}
	if err = boundedDimensions(config.Width, config.Height, l); err != nil {
		return nil, input, err
	}
	if input.Format == "png" {
		if err = validatePNG(data); err != nil {
			return nil, input, err
		}
	} else if err = validateJPEG(data); err != nil {
		return nil, input, err
	}
	input.Width, input.Height, input.Bytes = config.Width, config.Height, int64(len(data))
	return data, input, nil
}

// validatePNG checks every chunk, including ancillary CRCs that some native
// loaders ignore. Color hints are accepted only when they unambiguously mean
// sRGB or an embedded ICC profile takes precedence.
func validatePNG(data []byte) error {
	seenHeader, seenData, endedData := false, false, false
	hasICC, hasSRGB, unusualColor := false, false, false
	seen := map[string]bool{}
	for offset := 8; ; {
		if len(data)-offset < 12 {
			return errors.New("truncated PNG chunk")
		}
		length := uint64(binary.BigEndian.Uint32(data[offset:]))
		if length > uint64(len(data)-offset-12) {
			return errors.New("truncated PNG chunk data")
		}
		end := offset + 8 + int(length)
		kind := string(data[offset+4 : offset+8])
		payload := data[offset+8 : end]
		if crc32.ChecksumIEEE(data[offset+4:end]) != binary.BigEndian.Uint32(data[end:]) {
			return errors.New("invalid PNG checksum")
		}
		if !seenHeader && kind != "IHDR" {
			return errors.New("PNG does not begin with IHDR")
		}
		if kind != "IDAT" && seenData {
			endedData = true
		}
		switch kind {
		case "IHDR":
			if seenHeader || length != 13 {
				return errors.New("invalid PNG IHDR")
			}
			seenHeader = true
		case "IDAT":
			if endedData {
				return errors.New("non-contiguous PNG image data")
			}
			seenData = true
		case "IEND":
			if length != 0 || !seenData || end+4 != len(data) {
				return errors.New("invalid PNG end")
			}
			if unusualColor && !hasICC {
				return errors.New("unsupported PNG color hints")
			}
			if hasICC && hasSRGB {
				return errors.New("conflicting PNG color profiles")
			}
			return nil
		case "acTL", "fcTL", "fdAT":
			return errors.New("animated PNG is unsupported")
		case "iCCP", "sRGB", "gAMA", "cHRM":
			if seenData || seen[kind] {
				return errors.New("invalid PNG color chunk ordering")
			}
			seen[kind] = true
			switch kind {
			case "iCCP":
				nameEnd := bytes.IndexByte(payload, 0)
				if nameEnd < 1 || nameEnd > 79 || nameEnd+2 >= len(payload) || payload[nameEnd+1] != 0 {
					return errors.New("invalid PNG ICC chunk")
				}
				compressed := bytes.NewReader(payload[nameEnd+2:])
				reader, err := zlib.NewReader(compressed)
				if err != nil {
					return errors.New("invalid PNG ICC compression")
				}
				icc, err := io.ReadAll(io.LimitReader(reader, (4<<20)+1))
				closeErr := reader.Close()
				if err != nil || closeErr != nil || compressed.Len() != 0 || len(icc) > 4<<20 {
					return errors.New("invalid or oversized PNG ICC profile")
				}
				if err = validateICCHeader(icc); err != nil {
					return err
				}
				space := "RGB "
				if data[25] == 0 || data[25] == 4 {
					space = "GRAY"
				}
				if string(icc[16:20]) != space {
					return errors.New("incompatible PNG ICC colorspace")
				}
				hasICC = true
			case "sRGB":
				if len(payload) != 1 || payload[0] > 3 {
					return errors.New("invalid PNG sRGB chunk")
				}
				hasSRGB = true
			case "gAMA":
				if len(payload) != 4 {
					return errors.New("invalid PNG gamma")
				}
				unusualColor = unusualColor || binary.BigEndian.Uint32(payload) != 45455
			case "cHRM":
				if len(payload) != 32 {
					return errors.New("invalid PNG chromaticity")
				}
				srgb := []uint32{31270, 32900, 64000, 33000, 30000, 60000, 15000, 6000}
				for i, value := range srgb {
					unusualColor = unusualColor || binary.BigEndian.Uint32(payload[i*4:]) != value
				}
			}
		default:
			if len(kind) != 4 || (kind[0]&32 == 0 && kind != "PLTE") {
				return errors.New("unsupported PNG critical chunk")
			}
		}
		offset = end + 4
	}
}

func validateICCHeader(icc []byte) error {
	if len(icc) < 132 || len(icc) > 4<<20 ||
		uint64(binary.BigEndian.Uint32(icc)) != uint64(len(icc)) ||
		string(icc[36:40]) != "acsp" {
		return errors.New("invalid ICC header")
	}
	count := uint64(binary.BigEndian.Uint32(icc[128:]))
	if count > uint64((len(icc)-132)/12) {
		return errors.New("invalid ICC tag table")
	}
	for i := uint64(0); i < count; i++ {
		tag := icc[132+int(i)*12:]
		offset, size := uint64(binary.BigEndian.Uint32(tag[4:])), uint64(binary.BigEndian.Uint32(tag[8:]))
		if offset < 132+12*count || size < 8 || offset+size > uint64(len(icc)) {
			return errors.New("invalid ICC tag extent")
		}
	}
	return nil
}

func validateJPEG(data []byte) error {
	offset, scan := 2, false
	profileCount := 0
	profiles := make(map[int][]byte)
	for offset < len(data) {
		if data[offset] != 0xff {
			if scan {
				offset++
				continue
			}
			return errors.New("invalid JPEG marker")
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset == len(data) {
			break
		}
		marker := data[offset]
		offset++
		if scan && (marker == 0 || (marker >= 0xd0 && marker <= 0xd7)) {
			continue
		}
		if marker == 0xd9 {
			if !scan || offset != len(data) {
				return errors.New("invalid JPEG end or multiple images")
			}
			if len(profiles) != profileCount {
				return errors.New("incomplete JPEG ICC profile")
			}
			if profileCount > 0 {
				var icc []byte
				for i := 1; i <= profileCount; i++ {
					icc = append(icc, profiles[i]...)
				}
				return validateICCHeader(icc)
			}
			return nil
		}
		if marker != 0xc0 && marker != 0xc1 && marker != 0xc2 &&
			marker != 0xc4 && marker != 0xdb && marker != 0xdd && marker != 0xda &&
			marker != 0xfe && !(marker >= 0xe0 && marker <= 0xef) {
			return errors.New("unsupported JPEG encoding or marker")
		}
		if len(data)-offset < 2 {
			break
		}
		length := int(binary.BigEndian.Uint16(data[offset:]))
		if length < 2 || length > len(data)-offset {
			return errors.New("truncated JPEG segment")
		}
		payload := data[offset+2 : offset+length]
		if marker == 0xe2 && bytes.HasPrefix(payload, []byte("MPF\x00")) {
			return errors.New("multi-picture JPEG is unsupported")
		}
		if marker == 0xe2 && bytes.HasPrefix(payload, []byte("ICC_PROFILE\x00")) {
			if len(payload) < 14 {
				return errors.New("invalid JPEG ICC segment")
			}
			index, count := int(payload[12]), int(payload[13])
			if count == 0 || index == 0 || index > count || profiles[index] != nil ||
				(profileCount != 0 && count != profileCount) {
				return errors.New("invalid JPEG ICC sequence")
			}
			profileCount = count
			profiles[index] = payload[14:]
		}
		if marker == 0xda {
			scan = true
		}
		offset += length
	}
	return errors.New("truncated JPEG")
}

func load(path string, limits model.Limits) (*vips.ImageRef, model.Input, error) {
	data, input, err := readInput(path, limits)
	if err != nil {
		return nil, input, err
	}
	ref, err := normalize(data, input.Format)
	if err != nil {
		return nil, input, err
	}
	input.Width, input.Height = ref.Width(), ref.Height()
	if err = boundedDimensions(input.Width, input.Height, limits); err != nil {
		ref.Close()
		return nil, input, err
	}
	return ref, input, nil
}

// Validate checks the complete raster, not merely its file extension or header.
// Zero limits use conservative defaults; negative limits are invalid.
func Validate(path string, limits model.Limits) (model.Input, error) {
	limits, err := limitsWithDefaults(limits)
	if err != nil {
		return model.Input{}, err
	}
	ref, input, err := load(path, limits)
	if err != nil {
		return model.Input{}, err
	}
	ref.Close()
	return input, nil
}

// Widths returns sorted unique positive variant widths without upscaling.
func Widths(source int, profile model.Profile) []int {
	if source <= 0 {
		return nil
	}
	widths := make([]int, 0, len(profile.Widths)+1)
	for _, w := range profile.Widths {
		if w > 0 && w <= source {
			widths = append(widths, w)
		}
	}
	if profile.IncludeSource {
		widths = append(widths, source)
	}
	slices.Sort(widths)
	return slices.Compact(widths)
}

func variantFormats(profile model.Profile) []string {
	var formats []string
	for _, format := range profile.Formats {
		if (format == "webp" || format == "avif") && !slices.Contains(formats, format) {
			formats = append(formats, format)
		}
	}
	return formats
}

// Planned includes the mandatory full-size input-format output.
func Planned(input model.Input, profile model.Profile) int {
	if input.Width <= 0 || input.Height <= 0 || (input.Format != "jpeg" && input.Format != "png") {
		return 0
	}
	return 1 + len(Widths(input.Width, profile))*len(variantFormats(profile))
}

func checkedProfile(p model.Profile) (model.Profile, error) {
	for _, format := range p.Formats {
		if format != "webp" && format != "avif" {
			return p, errors.New("unsupported output format")
		}
	}
	if len(p.Widths) > 64 || len(p.Formats) > 64 {
		return p, errors.New("profile is too large")
	}
	for _, w := range p.Widths {
		if w <= 0 {
			return p, errors.New("variant widths must be positive")
		}
	}
	if p.JPEGQuality == 0 {
		p.JPEGQuality = 82
	}
	if p.WebPQuality == 0 {
		p.WebPQuality = 80
	}
	if p.AVIFQuality == 0 {
		p.AVIFQuality = 50
	}
	if p.JPEGQuality < 1 || p.JPEGQuality > 100 || p.WebPQuality < 1 || p.WebPQuality > 100 ||
		p.AVIFQuality < 1 || p.AVIFQuality > 100 || p.WebPEffort < 0 || p.WebPEffort > 6 ||
		p.AVIFEffort < 0 || p.AVIFEffort > 9 {
		return p, errors.New("invalid encoder options")
	}
	return p, nil
}

func encode(ref *vips.ImageRef, format string, p model.Profile) ([]byte, error) {
	var data []byte
	var err error
	switch format {
	case "jpeg":
		opts := vips.NewJpegExportParams()
		opts.Quality, opts.StripMetadata = p.JPEGQuality, true
		data, _, err = ref.ExportJpeg(opts)
	case "png":
		opts := vips.NewPngExportParams()
		opts.StripMetadata = true
		data, _, err = ref.ExportPng(opts)
	case "webp":
		opts := vips.NewWebpExportParams()
		opts.Quality, opts.ReductionEffort, opts.StripMetadata = p.WebPQuality, p.WebPEffort, true
		data, _, err = ref.ExportWebp(opts)
	case "avif":
		opts := vips.NewAvifExportParams()
		opts.Quality, opts.Effort, opts.StripMetadata = p.AVIFQuality, p.AVIFEffort, true
		data, _, err = ref.ExportAvif(opts)
	default:
		err = errors.New("unsupported encoder")
	}
	if err == nil && len(data) == 0 {
		err = errors.New("encoder returned no bytes")
	}
	return data, err
}

// Process generates each output directly from one oriented, color-normalized,
// metadata-free in-memory source. It removes its own partial outputs on error.
func Process(task model.Task) (outputs []model.Output, err error) {
	limits, err := limitsWithDefaults(task.Limits)
	if err != nil {
		return nil, err
	}
	profile, err := checkedProfile(task.Profile)
	if err != nil {
		return nil, err
	}
	if task.OutputDir == "" {
		return nil, errors.New("output directory is required")
	}
	source, input, err := load(task.InputPath, limits)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	if Planned(input, profile) > limits.OutputCount {
		return nil, errors.New("output count exceeds limit")
	}
	if err = os.MkdirAll(task.OutputDir, 0700); err != nil {
		return nil, err
	}
	var created []string
	defer func() {
		if err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
			outputs = nil
		}
	}()
	var total int64
	write := func(format string, width int) error {
		ref, e := source.Copy()
		if e != nil {
			return e
		}
		defer ref.Close()
		if width != input.Width {
			// Filter premultiplied samples so hidden colors cannot bleed into
			// transparent edges. The normalized source remains untouched.
			if ref.HasAlpha() {
				if e = ref.PremultiplyAlpha(); e != nil {
					return e
				}
			}
			if e = ref.Resize(float64(width)/float64(input.Width), vips.KernelLanczos3); e != nil {
				return e
			}
			if ref.HasAlpha() {
				if e = ref.UnpremultiplyAlpha(); e != nil {
					return e
				}
			}
		}
		data, e := encode(ref, format, profile)
		if e != nil {
			return fmt.Errorf("encode %s: %w", format, e)
		}
		if int64(len(data)) > limits.OutputBytes-total {
			return errors.New("aggregate output bytes exceed limit")
		}
		name := fmt.Sprintf("%s-%d", format, width)
		path := filepath.Join(task.OutputDir, name+"."+format)
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		created = append(created, path)
		_, e = file.Write(data)
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		total += int64(len(data))
		outputs = append(outputs, model.Output{
			ID: name, Format: format, Width: ref.Width(), Height: ref.Height(),
			Bytes: int64(len(data)), Path: path,
			SizeDifferencePercent: 100 * (float64(len(data))/float64(input.Bytes) - 1),
			Smaller:               int64(len(data)) < input.Bytes,
			IncludesResizing:      ref.Width() != input.Width || ref.Height() != input.Height,
		})
		return nil
	}
	if err = write(input.Format, input.Width); err != nil {
		return nil, err
	}
	for _, format := range variantFormats(profile) {
		for _, width := range Widths(input.Width, profile) {
			if err = write(format, width); err != nil {
				return nil, err
			}
		}
	}
	return outputs, nil
}

// Probe performs actual encode-and-full-decode checks for every required format,
// including AVIF; advertised loader names alone do not prove an encoder exists.
func Probe() error {
	probeOnce.Do(func() {
		if probeErr = startup(); probeErr != nil {
			return
		}
		ref, err := vips.NewImageFromMemory(bytes.Repeat([]byte{80, 120, 180}, 16*16),
			16, 16, 3, vips.BandFormatUchar, vips.InterpretationSRGB)
		if err != nil {
			probeErr = err
			return
		}
		defer ref.Close()
		profile, _ := checkedProfile(model.Profile{})
		for _, format := range []string{"jpeg", "png", "webp", "avif"} {
			capability := Capability{Format: format, Input: format == "jpeg" || format == "png", Output: true}
			data, e := encode(ref, format, profile)
			if e == nil {
				var decoded *vips.ImageRef
				decoded, e = vips.LoadImageFromBuffer(data, nil)
				if e == nil {
					if decoded.Width() != 16 || decoded.Height() != 16 {
						e = errors.New("codec dimensions differ")
					}
					if e == nil {
						_, e = decoded.ToBytes()
					}
					decoded.Close()
				}
			}
			capability.Available = e == nil
			capabilities = append(capabilities, capability)
			if e != nil {
				probeErr = errors.Join(probeErr, fmt.Errorf("%s codec smoke test: %w", format, e))
			}
		}
	})
	return probeErr
}

// Formats returns a detached copy of the probed capabilities.
func Formats() []Capability {
	_ = Probe()
	return slices.Clone(capabilities)
}
