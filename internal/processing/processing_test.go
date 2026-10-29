package processing

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/openbracketdevelopment/ByteDown/internal/model"
)

func testDir(t *testing.T) string {
	t.Helper()
	// Keep scratch files in the package, not the host temporary directory.
	dir, err := os.MkdirTemp(".", ".processing-test-")
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(absolute) })
	return absolute
}

func put(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T, format string, w, h int, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if alpha && x < w/2 {
				a = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(40 + x*150/w), uint8(60 + y*140/h), 120, a})
		}
	}
	var data bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&data, img, &jpeg.Options{Quality: 95})
	} else {
		err = png.Encode(&data, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func profile() model.Profile {
	return model.Profile{
		Widths: []int{8, 16, 16, 1000}, Formats: []string{"webp", "avif"},
		IncludeSource: true, JPEGQuality: 82, WebPQuality: 80, WebPEffort: 1,
		AVIFQuality: 50, AVIFEffort: 0,
	}
}

func pngChunk(kind string, payload []byte) []byte {
	data := make([]byte, len(payload)+12)
	binary.BigEndian.PutUint32(data, uint32(len(payload)))
	copy(data[4:], kind)
	copy(data[8:], payload)
	binary.BigEndian.PutUint32(data[len(data)-4:], crc32.ChecksumIEEE(data[4:len(data)-4]))
	return data
}

func addPNGChunk(data []byte, kind string, payload []byte) []byte {
	out := append([]byte{}, data[:33]...)
	out = append(out, pngChunk(kind, payload)...)
	return append(out, data[33:]...)
}

func jpegSegment(marker byte, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	out[0], out[1] = 0xff, marker
	binary.BigEndian.PutUint16(out[2:], uint16(len(payload)+2))
	copy(out[4:], payload)
	return out
}

func orientedJPEG(data []byte, orientation byte) []byte {
	exif := []byte{
		'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0,
		1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, orientation, 0, 0, 0,
		0, 0, 0, 0,
	}
	out := append([]byte{}, data[:2]...)
	out = append(out, jpegSegment(0xe1, exif)...)
	return append(out, data[2:]...)
}

func decodeOutput(t *testing.T, output model.Output) *vips.ImageRef {
	t.Helper()
	data, err := os.ReadFile(output.Path)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := vips.LoadImageFromBuffer(data, nil)
	if err != nil {
		t.Fatalf("%s decode: %v", output.Format, err)
	}
	t.Cleanup(ref.Close)
	if _, err = ref.ToBytes(); err != nil {
		t.Fatalf("%s full decode: %v", output.Format, err)
	}
	return ref
}

func TestProbeAndCapabilities(t *testing.T) {
	if err := Probe(); err != nil {
		t.Fatal(err)
	}
	formats := Formats()
	if len(formats) != 4 {
		t.Fatalf("capabilities: %+v", formats)
	}
	for i, name := range []string{"jpeg", "png", "webp", "avif"} {
		if formats[i].Format != name || formats[i].Identifier != name || !formats[i].Available || !formats[i].Output ||
			formats[i].Input != (name == "jpeg" || name == "png") {
			t.Fatalf("unexpected capability: %+v", formats[i])
		}
	}
	formats[0].Format = "mutated"
	if Formats()[0].Format != "jpeg" {
		t.Fatal("capabilities share mutable storage")
	}
	formats[0].Options[0].Default = 1
	if Formats()[0].Options[0].Default != 82 {
		t.Fatal("encoder options share mutable storage")
	}
	for _, format := range Formats() {
		if format.Extension == "" || format.MIME != "image/"+format.Format ||
			format.Transparency != (format.Format != "jpeg") {
			t.Fatalf("incomplete capability metadata: %+v", format)
		}
		name, err := OutputFilename(format.Format, 480)
		if err != nil || name != format.Format+"-480."+format.Extension {
			t.Fatalf("filename/metadata contract mismatch: %s %v", name, err)
		}
	}
	for _, invalid := range []struct {
		format string
		width  int
	}{{"../png", 480}, {"gif", 480}, {"png", 0}, {"png", -1}} {
		if _, err := OutputFilename(invalid.format, invalid.width); err == nil {
			t.Fatalf("invalid output filename accepted: %+v", invalid)
		}
	}
}

func TestWidthsAndPlanned(t *testing.T) {
	tests := []struct {
		source int
		p      model.Profile
		want   []int
	}{
		{32, profile(), []int{8, 16, 32}},
		{32, model.Profile{Widths: []int{32, 1, 16, 0, -1, 33, 1}}, []int{1, 16, 32}},
		{4, model.Profile{IncludeSource: true, Widths: []int{8}}, []int{4}},
		{0, profile(), nil},
	}
	for _, tt := range tests {
		if got := Widths(tt.source, tt.p); !slices.Equal(got, tt.want) {
			t.Fatalf("widths %v, want %v", got, tt.want)
		}
	}
	p := profile()
	original := slices.Clone(p.Widths)
	if got := Planned(model.Input{Format: "png", Width: 32, Height: 16}, p); got != 7 {
		t.Fatalf("planned %d", got)
	}
	p.Formats = append(p.Formats, "avif")
	if got := Planned(model.Input{Format: "jpeg", Width: 32, Height: 16}, p); got != 7 {
		t.Fatalf("duplicate formats count: %d", got)
	}
	if !slices.Equal(p.Widths, original) {
		t.Fatal("profile mutated")
	}
	if Planned(model.Input{}, p) != 0 {
		t.Fatal("invalid input planned")
	}
	p.IncludeSource = false
	if Planned(model.Input{Format: "png", Width: 32, Height: 16}, p) != 5 {
		t.Fatal("source excluded count")
	}
}

func TestNativeConcurrencyConfiguration(t *testing.T) {
	original, configured := os.LookupEnv("VIPS_CONCURRENCY")
	if err := os.Unsetenv("VIPS_CONCURRENCY"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if configured {
			_ = os.Setenv("VIPS_CONCURRENCY", original)
		} else {
			_ = os.Unsetenv("VIPS_CONCURRENCY")
		}
	})
	if got, err := nativeConcurrency(); err != nil || got != 1 {
		t.Fatalf("default concurrency: %d %v", got, err)
	}
	for _, test := range []struct {
		value string
		want  int
	}{{"1", 1}, {"16", 16}, {"4", 4}, {"0", 0}, {"17", 0}, {"-1", 0},
		{"", 0}, {"not-a-number", 0}, {"1.5", 0}, {" 1", 0}, {"999999999999999999999999", 0}} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("VIPS_CONCURRENCY", test.value)
			got, err := nativeConcurrency()
			if got != test.want || (err != nil) != (test.want == 0) {
				t.Fatalf("configured %q: %d %v", test.value, got, err)
			}
			if err != nil && !strings.Contains(err.Error(), "VIPS_CONCURRENCY") {
				t.Fatal("configuration error lacks the setting name")
			}
		})
	}
}

func TestValidateSniffsAndChecksLimits(t *testing.T) {
	dir := testDir(t)
	for _, format := range []string{"jpeg", "png"} {
		path := put(t, dir, format+".not-an-image", fixture(t, format, 32, 16, false))
		input, err := Validate(path, model.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if input.Format != format || input.Width != 32 || input.Height != 16 ||
			input.EncodedWidth != 32 || input.EncodedHeight != 16 || input.Bytes <= 0 {
			t.Fatalf("input: %+v", input)
		}
		for _, limits := range []model.Limits{
			{UploadBytes: input.Bytes - 1}, {Dimension: 31}, {Pixels: 511}, {UploadBytes: -1},
		} {
			if _, err := Validate(path, limits); err == nil {
				t.Fatalf("%s accepted limits %+v", format, limits)
			}
		}
		if _, err := Validate(path, model.Limits{UploadBytes: input.Bytes, Dimension: 32, Pixels: 512}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Validate(dir, model.Limits{}); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestOrientationAllEight(t *testing.T) {
	dir := testDir(t)
	base := fixture(t, "jpeg", 40, 24, false)
	plain, err := normalize(base, "jpeg")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	for orientation := byte(1); orientation <= 8; orientation++ {
		t.Run(string(rune('0'+orientation)), func(t *testing.T) {
			data := orientedJPEG(base, orientation)
			path := put(t, dir, "orientation-"+string(rune('0'+orientation)), data)
			input, err := Validate(path, model.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			w, h := 40, 24
			if orientation >= 5 {
				w, h = h, w
			}
			if input.Width != w || input.Height != h {
				t.Fatalf("orientation %d: %+v", orientation, input)
			}
			if input.EncodedWidth != 40 || input.EncodedHeight != 24 {
				t.Fatalf("orientation %d lost encoded dimensions: %+v", orientation, input)
			}
			ref, err := normalize(data, "jpeg")
			if err != nil {
				t.Fatal(err)
			}
			defer ref.Close()
			// Compare actual corner pixels, including the mirrored orientations.
			coordinates := map[byte][2]int{1: {0, 0}, 2: {39, 0}, 3: {39, 23}, 4: {0, 23}, 5: {0, 0}, 6: {0, 23}, 7: {39, 23}, 8: {39, 0}}
			point := coordinates[orientation]
			want, err := plain.GetPoint(point[0], point[1])
			if err != nil {
				t.Fatal(err)
			}
			got, err := ref.GetPoint(0, 0)
			if err != nil {
				t.Fatal(err)
			}
			for i := range want {
				if math.Abs(want[i]-got[i]) > 1 {
					t.Fatalf("orientation pixels %v want %v", got, want)
				}
			}
		})
	}
	path := put(t, dir, "rotate.jpg", orientedJPEG(base, 6))
	outputs, err := Process(model.Task{InputPath: path, OutputDir: filepath.Join(dir, "oriented"), Profile: profile()})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range outputs {
		ref := decodeOutput(t, output)
		if ref.Orientation() > 1 || len(ref.GetExif()) != 0 || ref.HasICCProfile() {
			t.Fatalf("metadata retained: %+v", ref.Metadata())
		}
		if output.Width > 24 {
			t.Fatal("upscaled oriented image")
		}
		if !output.IncludesResizing && (output.Width != 24 || output.Height != 40) {
			t.Fatalf("resize label: %+v", output)
		}
	}
}

func TestProcessFormatsAlphaAndMetrics(t *testing.T) {
	dir := testDir(t)
	for _, format := range []string{"jpeg", "png"} {
		t.Run(format, func(t *testing.T) {
			path := put(t, dir, format, fixture(t, format, 32, 16, format == "png"))
			input, err := Validate(path, model.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			outputDir := filepath.Join(dir, "outputs-"+format)
			outputs, err := Process(model.Task{InputPath: path, OutputDir: outputDir, Profile: profile()})
			if err != nil {
				t.Fatal(err)
			}
			if len(outputs) != Planned(input, profile()) {
				t.Fatalf("outputs: %d", len(outputs))
			}
			seen := map[string]bool{}
			for _, output := range outputs {
				ref := decodeOutput(t, output)
				if ref.Width() != output.Width || ref.Height() != output.Height {
					t.Fatal("dimension metadata differs")
				}
				if seen[output.ID] {
					t.Fatalf("duplicate output %s", output.ID)
				}
				seen[output.ID] = true
				wantName := output.ID + "." + output.Format
				if output.Format == "jpeg" {
					wantName = output.ID + ".jpg"
				}
				if output.Path != filepath.Join(outputDir, wantName) || !strings.HasPrefix(output.Path, outputDir+string(filepath.Separator)) {
					t.Fatalf("unsafe output: %+v", output)
				}
				stat, err := os.Stat(output.Path)
				if err != nil {
					t.Fatal(err)
				}
				if output.Bytes != stat.Size() {
					t.Fatal("size metadata differs")
				}
				wantDifference := 100 * (1 - float64(output.Bytes)/float64(input.Bytes))
				if math.Abs(output.SizeDifferencePercent-wantDifference) > 1e-8 || output.Smaller != (output.Bytes < input.Bytes) {
					t.Fatalf("savings: %+v", output)
				}
				if output.IncludesResizing != (output.Width != input.Width || output.Height != input.Height) {
					t.Fatal("resize flag differs")
				}
				if format == "png" {
					if !ref.HasAlpha() {
						t.Fatalf("%s lost alpha", output.Format)
					}
					point, err := ref.GetPoint(0, ref.Height()/2)
					if err != nil {
						t.Fatal(err)
					}
					if point[len(point)-1] > 3 {
						t.Fatalf("%s changed transparency: %v", output.Format, point)
					}
					point, err = ref.GetPoint(ref.Width()-1, ref.Height()/2)
					if err != nil {
						t.Fatal(err)
					}
					if point[len(point)-1] < 252 {
						t.Fatalf("%s changed opacity: %v", output.Format, point)
					}
				}
			}
			// Existing deterministic names are never overwritten.
			before, err := os.ReadFile(outputs[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Process(model.Task{InputPath: path, OutputDir: outputDir, Profile: profile()}); err == nil {
				t.Fatal("existing output overwritten")
			}
			after, err := os.ReadFile(outputs[0].Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("existing output damaged")
			}
		})
	}
}

func TestOutputLimitsAndCleanup(t *testing.T) {
	dir := testDir(t)
	path := put(t, dir, "input.png", fixture(t, "png", 32, 16, true))
	for name, limits := range map[string]model.Limits{
		"count": {OutputCount: 6}, "bytes": {OutputBytes: 1}, "negative": {OutputCount: -1},
	} {
		out := filepath.Join(dir, name)
		if outputs, err := Process(model.Task{InputPath: path, OutputDir: out, Profile: profile(), Limits: limits}); err == nil || len(outputs) != 0 {
			t.Fatalf("%s accepted", name)
		}
		entries, err := os.ReadDir(out)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s leaked outputs", name)
		}
	}
	baseline, err := Process(model.Task{InputPath: path, OutputDir: filepath.Join(dir, "baseline"), Profile: profile()})
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, output := range baseline {
		total += output.Bytes
	}
	for _, test := range []struct {
		name    string
		limit   int64
		success bool
	}{{"exact", total, true}, {"aggregate", total - 1, false}} {
		outputDir := filepath.Join(dir, test.name)
		outputs, err := Process(model.Task{InputPath: path, OutputDir: outputDir, Profile: profile(), Limits: model.Limits{OutputBytes: test.limit, OutputCount: 7}})
		if (err == nil) != test.success {
			t.Fatalf("%s: %v", test.name, err)
		}
		if !test.success {
			entries, _ := os.ReadDir(outputDir)
			if len(outputs) > 0 || len(entries) > 0 {
				t.Fatal("partial output cleanup failed")
			}
		}
	}
	for _, p := range []model.Profile{
		{Formats: []string{"../../x"}}, {Widths: []int{-1}}, {WebPQuality: 101}, {AVIFEffort: 10}, {WebPEffort: 7},
	} {
		if _, err := Process(model.Task{InputPath: path, OutputDir: filepath.Join(dir, "bad"), Profile: p}); err == nil {
			t.Fatalf("invalid profile accepted: %+v", p)
		}
	}
}

func TestRejectUnsupportedAnimatedAndCorrupt(t *testing.T) {
	dir := testDir(t)
	pngData := fixture(t, "png", 32, 16, true)
	jpegData := fixture(t, "jpeg", 32, 16, false)
	badCRC := append([]byte{}, pngData...)
	badCRC[len(badCRC)-5] ^= 1
	anim := make([]byte, 8)
	binary.BigEndian.PutUint32(anim, 2)
	gamma := make([]byte, 4)
	binary.BigEndian.PutUint32(gamma, 100000)
	tests := map[string][]byte{
		"empty": nil, "gif": []byte("GIF89a"), "svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"fake-jpeg": []byte{0xff, 0xd8, 0xff, 0xd9}, "jpeg-truncated": jpegData[:len(jpegData)-2],
		"jpeg-trailing": append(append([]byte{}, jpegData...), 1),
		"jpeg-multiple": append(append([]byte{}, jpegData...), jpegData...),
		"png-truncated": pngData[:len(pngData)-12], "png-crc": badCRC,
		"png-trailing":    append(append([]byte{}, pngData...), 0),
		"apng":            addPNGChunk(pngData, "acTL", anim),
		"apng-frame":      addPNGChunk(pngData, "fcTL", make([]byte, 26)),
		"png-gamma":       addPNGChunk(pngData, "gAMA", gamma),
		"png-invalid-icc": addPNGChunk(pngData, "iCCP", []byte("broken\x00\x00bad-zlib")),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			path := put(t, dir, name, data)
			if _, err := Validate(path, model.Limits{}); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
	// Keep valid JPEG framing while destroying the entropy-coded raster.
	index := bytes.Index(jpegData, []byte{0xff, 0xda})
	if index < 0 {
		t.Fatal("no JPEG scan")
	}
	start := index + 2 + int(binary.BigEndian.Uint16(jpegData[index+2:]))
	corrupt := append([]byte{}, jpegData...)
	copy(corrupt[start:], bytes.Repeat([]byte{0xff, 0}, (len(corrupt)-start)/2))
	corrupt[len(corrupt)-2], corrupt[len(corrupt)-1] = 0xff, 0xd9
	if _, err := Validate(put(t, dir, "entropy.jpg", corrupt), model.Limits{}); err == nil {
		t.Fatal("corrupt JPEG entropy accepted")
	}
	for _, name := range []string{"png-deflate-corrupt", "png-deflate-short", "png-short-raster"} {
		var changed []byte
		for offset := 8; offset < len(pngData); {
			length := int(binary.BigEndian.Uint32(pngData[offset:]))
			kind := string(pngData[offset+4 : offset+8])
			payload := append([]byte{}, pngData[offset+8:offset+8+length]...)
			if offset == 8 {
				changed = append(changed, pngData[:8]...)
			}
			if kind == "IDAT" {
				switch name {
				case "png-deflate-corrupt":
					payload[len(payload)/2] ^= 0x7f
				case "png-deflate-short":
					payload = payload[:len(payload)/2]
				case "png-short-raster":
					var short bytes.Buffer
					writer := zlib.NewWriter(&short)
					_, _ = writer.Write([]byte{0, 0, 0, 0, 0})
					_ = writer.Close()
					payload = short.Bytes()
				}
			}
			changed = append(changed, pngChunk(kind, payload)...)
			offset += length + 12
		}
		if _, err := Validate(put(t, dir, name, changed), model.Limits{}); err == nil {
			t.Fatalf("accepted %s with repaired chunk CRCs", name)
		}
	}
	// A valid PNG header with huge dimensions must be rejected before attempting
	// to decode its deliberately missing raster.
	bomb := append([]byte{}, pngData[:33]...)
	binary.BigEndian.PutUint32(bomb[16:], 100000)
	binary.BigEndian.PutUint32(bomb[20:], 100000)
	binary.BigEndian.PutUint32(bomb[29:], crc32.ChecksumIEEE(bomb[12:29]))
	if _, err := Validate(put(t, dir, "bomb.png", bomb), model.Limits{}); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("pixel bomb did not fail before decode: %v", err)
	}
}

func TestPNGExifOrientation(t *testing.T) {
	dir := testDir(t)
	exifJPEG := orientedJPEG(fixture(t, "jpeg", 40, 24, false), 6)
	length := int(binary.BigEndian.Uint16(exifJPEG[4:]))
	exif := exifJPEG[12 : 4+length] // PNG eXIf contains TIFF, without Exif NUL NUL.
	data := addPNGChunk(fixture(t, "png", 40, 24, true), "eXIf", exif)
	input, err := Validate(put(t, dir, "oriented.png", data), model.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if input.Width != 24 || input.Height != 40 || input.EncodedWidth != 40 || input.EncodedHeight != 24 {
		t.Fatalf("PNG orientation ignored: %+v", input)
	}
}

func TestEveryResizeUsesNormalizedSource(t *testing.T) {
	dir := testDir(t)
	path := put(t, dir, "source.png", fixture(t, "png", 91, 47, true))
	p := profile()
	p.Widths = []int{8, 29, 61}
	p.Formats = []string{"webp"}
	outputs, err := Process(model.Task{InputPath: path, OutputDir: filepath.Join(dir, "out"), Profile: p})
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := limitsWithDefaults(model.Limits{})
	source, _, err := load(path, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, output := range outputs {
		if output.Format != "webp" || !output.IncludesResizing {
			continue
		}
		direct, err := source.Copy()
		if err != nil {
			t.Fatal(err)
		}
		if err = direct.PremultiplyAlpha(); err == nil {
			err = direct.Resize(float64(output.Width)/91, vips.KernelLanczos3)
		}
		if err == nil {
			err = direct.UnpremultiplyAlpha()
		}
		if err != nil {
			direct.Close()
			t.Fatal(err)
		}
		expected, err := encode(direct, "webp", p)
		direct.Close()
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(output.Path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("width %d was not encoded directly from normalized source", output.Width)
		}
	}
}

// These tiny, synthetic ICC fixtures are constructed here instead of depending
// on profiles installed on the test host. The RGB profile has linear transfer;
// the CMYK profile uses a deliberately simple four-channel LUT to D50 XYZ.
func fixed(value float64) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, uint32(int32(math.Round(value*65536))))
	return out
}

func xyz(x, y, z float64) []byte {
	data := append([]byte("XYZ \x00\x00\x00\x00"), fixed(x)...)
	data = append(data, fixed(y)...)
	return append(data, fixed(z)...)
}

func iccProfile(cmyk bool) []byte {
	tags := map[string][]byte{"wtpt": xyz(.9642, 1, .8249)}
	order := []string{"wtpt"}
	space, class := "RGB ", "mntr"
	if !cmyk {
		tags["rXYZ"], tags["gXYZ"], tags["bXYZ"] = xyz(.4360747, .2225045, .0139322), xyz(.3850649, .7168786, .0971045), xyz(.1430804, .0606169, .7141733)
		for _, name := range []string{"rTRC", "gTRC", "bTRC"} {
			tags[name] = []byte{'c', 'u', 'r', 'v', 0, 0, 0, 0, 0, 0, 0, 1, 1, 0, 0, 0}
		}
		order = append(order, "rXYZ", "gXYZ", "bXYZ", "rTRC", "gTRC", "bTRC")
	} else {
		space, class = "CMYK", "scnr"
		lut := make([]byte, 48)
		copy(lut, "mft1")
		lut[8], lut[9], lut[10] = 4, 3, 2
		for i := 0; i < 3; i++ {
			copy(lut[12+4*(i*3+i):], fixed(1))
		}
		for c := 0; c < 4; c++ {
			for i := 0; i < 256; i++ {
				lut = append(lut, byte(i))
			}
		}
		for c := 0; c < 2; c++ {
			for m := 0; m < 2; m++ {
				for y := 0; y < 2; y++ {
					for k := 0; k < 2; k++ {
						r, g, b := float64((1-c)*(1-k)), float64((1-m)*(1-k)), float64((1-y)*(1-k))
						for _, value := range []float64{.4360747*r + .3850649*g + .1430804*b, .2225045*r + .7168786*g + .0606169*b, .0139322*r + .0971045*g + .7141733*b} {
							lut = append(lut, byte(math.Round(value*128)))
						}
					}
				}
			}
		}
		for c := 0; c < 3; c++ {
			for i := 0; i < 256; i++ {
				lut = append(lut, byte(i))
			}
		}
		tags["A2B0"] = lut
		order = append(order, "A2B0")
	}
	out := make([]byte, 132+12*len(order))
	copy(out[4:], "test")
	binary.BigEndian.PutUint32(out[8:], 0x02100000)
	copy(out[12:], class)
	copy(out[16:], space)
	copy(out[20:], "XYZ ")
	copy(out[36:], "acsp")
	copy(out[40:], "MSFT")
	binary.BigEndian.PutUint32(out[64:], 1)
	copy(out[68:], fixed(.9642))
	copy(out[72:], fixed(1))
	copy(out[76:], fixed(.8249))
	binary.BigEndian.PutUint32(out[128:], uint32(len(order)))
	for i, name := range order {
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		index := 132 + i*12
		copy(out[index:], name)
		binary.BigEndian.PutUint32(out[index+4:], uint32(len(out)))
		binary.BigEndian.PutUint32(out[index+8:], uint32(len(tags[name])))
		out = append(out, tags[name]...)
	}
	binary.BigEndian.PutUint32(out, uint32(len(out)))
	return out
}

func withPNGICC(t *testing.T, data, icc []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(icc); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("synthetic\x00\x00"), compressed.Bytes()...)
	return addPNGChunk(data, "iCCP", payload)
}

func withJPEGICC(data, icc []byte) []byte {
	payload := append([]byte("ICC_PROFILE\x00\x01\x01"), icc...)
	out := append([]byte{}, data[:2]...)
	out = append(out, jpegSegment(0xe2, payload)...)
	return append(out, data[2:]...)
}

func TestColorManagementAndCMYK(t *testing.T) {
	dir := testDir(t)
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{128, 128, 128, 128})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	tagged := withPNGICC(t, buffer.Bytes(), iccProfile(false))
	path := put(t, dir, "linear-profile.png", tagged)
	outputs, err := Process(model.Task{InputPath: path, OutputDir: filepath.Join(dir, "rgb"), Profile: profile()})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range outputs {
		ref := decodeOutput(t, output)
		point, err := ref.GetPoint(ref.Width()/2, ref.Height()/2)
		if err != nil {
			t.Fatal(err)
		}
		if point[0] < 180 || point[0] > 195 {
			t.Fatalf("%s failed ICC conversion: %v", output.Format, point)
		}
		if len(point) != 4 || math.Abs(point[3]-128) > 3 {
			t.Fatalf("%s ICC changed alpha: %v", output.Format, point)
		}
		if ref.HasICCProfile() {
			t.Fatal("source ICC retained after sRGB conversion")
		}
	}
	if err := startup(); err != nil {
		t.Fatal(err)
	}
	cmyk, err := vips.NewImageFromMemory(bytes.Repeat([]byte{0, 0, 0, 255}, 16*16), 16, 16, 4, vips.BandFormatUchar, vips.InterpretationCMYK)
	if err != nil {
		t.Fatal(err)
	}
	defer cmyk.Close()
	params := vips.NewJpegExportParams()
	params.StripMetadata = true
	cmykJPEG, _, err := cmyk.ExportJpeg(params)
	if err != nil {
		t.Fatal(err)
	}
	rawCMYK, err := vips.LoadImageFromBuffer(cmykJPEG, nil)
	if err != nil {
		t.Fatal(err)
	}
	rawPoint, err := rawCMYK.GetPoint(0, 0)
	rawCMYK.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rawPoint, []float64{0, 0, 0, 255}) {
		t.Fatalf("invalid CMYK fixture: %v", rawPoint)
	}
	if _, err := Validate(put(t, dir, "untagged-cmyk.jpg", cmykJPEG), model.Limits{}); err == nil {
		t.Fatal("untagged CMYK guessed")
	}
	taggedCMYK := withJPEGICC(cmykJPEG, iccProfile(true))
	cmykPath := put(t, dir, "tagged-cmyk.jpg", taggedCMYK)
	if _, err := Validate(cmykPath, model.Limits{}); err != nil {
		t.Fatalf("profiled CMYK: %v", err)
	}
	cmykOutputs, err := Process(model.Task{InputPath: cmykPath, OutputDir: filepath.Join(dir, "cmyk"), Profile: profile()})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range cmykOutputs {
		ref := decodeOutput(t, output)
		point, err := ref.GetPoint(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(point) != 3 || point[0] > 5 || point[1] > 5 || point[2] > 5 {
			t.Fatalf("%s CMYK black not converted: %v", output.Format, point)
		}
	}
	bad := iccProfile(false)
	copy(bad[16:], "Lab ")
	for name, data := range map[string][]byte{
		"incompatible-space": withPNGICC(t, buffer.Bytes(), bad),
		"wrong-cmyk-profile": withJPEGICC(cmykJPEG, iccProfile(false)),
		"broken-profile":     withPNGICC(t, buffer.Bytes(), []byte("not an ICC profile")),
	} {
		if _, err := Validate(put(t, dir, name, data), model.Limits{}); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestGrayAndSixteenBit(t *testing.T) {
	dir := testDir(t)
	gray := image.NewGray(image.Rect(0, 0, 8, 8))
	gray16 := image.NewGray16(image.Rect(0, 0, 8, 8))
	rgba16 := image.NewNRGBA64(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			gray.SetGray(x, y, color.Gray{Y: 128})
			gray16.SetGray16(x, y, color.Gray16{Y: 32768})
			rgba16.SetNRGBA64(x, y, color.NRGBA64{R: 32768, G: 32768, B: 32768, A: 32768})
		}
	}
	for _, img := range []image.Image{gray, gray16, rgba16} {
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, img); err != nil {
			t.Fatal(err)
		}
		path := put(t, dir, "gray.png", buffer.Bytes())
		input, err := Validate(path, model.Limits{})
		if err != nil || input.Width != 8 || input.Height != 8 {
			t.Fatalf("gray/16-bit: %+v %v", input, err)
		}
		ref, err := normalize(buffer.Bytes(), "png")
		if err != nil {
			t.Fatal(err)
		}
		point, err := ref.GetPoint(0, 0)
		ref.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, sample := range point {
			if math.Abs(sample-128) > 1 {
				t.Fatalf("gray/16-bit samples incorrectly scaled: %v", point)
			}
		}
	}
}

// Opt-in review artifacts intentionally survive this test for visual inspection.
func TestRepresentativeRenderings(t *testing.T) {
	if os.Getenv("BYTEDOWN_REVIEW") != "1" {
		t.Skip("set BYTEDOWN_REVIEW=1 to preserve visual review images")
	}
	dir, err := filepath.Abs(".review")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	savePNG := func(name string, ref *vips.ImageRef) {
		t.Helper()
		opts := vips.NewPngExportParams()
		opts.Compression, opts.StripMetadata = 9, true
		data, _, err := ref.ExportPng(opts)
		if err != nil {
			t.Fatal(err)
		}
		put(t, dir, name, data)
	}
	run := func(name string, data []byte, p model.Profile) []model.Output {
		t.Helper()
		path := put(t, dir, name+"-input", data)
		outputDir := filepath.Join(dir, name+"-outputs")
		if err := os.RemoveAll(outputDir); err != nil {
			t.Fatal(err)
		}
		outputs, err := Process(model.Task{InputPath: path, OutputDir: outputDir, Profile: p})
		if err != nil {
			t.Fatal(err)
		}
		return outputs
	}
	p := profile()
	p.Widths = []int{64, 128}
	orientation := image.NewNRGBA(image.Rect(0, 0, 240, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 240; x++ {
			c := color.NRGBA{220, 40, 35, 255}
			if x >= 120 {
				c = color.NRGBA{40, 190, 65, 255}
			}
			if y >= 80 {
				c = color.NRGBA{35, 75, 220, 255}
				if x >= 120 {
					c = color.NRGBA{245, 200, 30, 255}
				}
			}
			if x < 20 && y < 20 {
				c = color.NRGBA{255, 255, 255, 255}
			}
			orientation.SetNRGBA(x, y, c)
		}
	}
	var encoded bytes.Buffer
	if err = jpeg.Encode(&encoded, orientation, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	outputs := run("orientation", orientedJPEG(encoded.Bytes(), 6), p)
	savePNG("orientation.png", decodeOutput(t, outputs[0]))

	alpha := image.NewNRGBA(image.Rect(0, 0, 256, 192))
	for y := 0; y < 192; y++ {
		for x := 0; x < 256; x++ {
			distance := math.Hypot(float64(x-128), float64(y-96))
			a := uint8(math.Round(math.Max(0, math.Min(1, (88-distance)/16)) * 255))
			alpha.SetNRGBA(x, y, color.NRGBA{uint8(40 + x*180/256), 70, 220, a})
		}
	}
	encoded.Reset()
	if err = png.Encode(&encoded, alpha); err != nil {
		t.Fatal(err)
	}
	outputs = run("transparency", encoded.Bytes(), p)
	savePNG("transparency.png", decodeOutput(t, outputs[0]))
	for _, output := range outputs {
		if output.Format == "avif" && output.Width == 64 {
			savePNG("transparency-avif-64.png", decodeOutput(t, output))
		}
	}

	linear := image.NewNRGBA(image.Rect(0, 0, 256, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 256; x++ {
			c := color.NRGBA{uint8(x), uint8(x), uint8(x), 255}
			if y >= 64 {
				c = color.NRGBA{uint8(x), uint8(y * 2), uint8(255 - x), 255}
			}
			linear.SetNRGBA(x, y, c)
		}
	}
	encoded.Reset()
	if err = png.Encode(&encoded, linear); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "color-linear-before.png", encoded.Bytes())
	outputs = run("color", withPNGICC(t, encoded.Bytes(), iccProfile(false)), p)
	savePNG("color-srgb.png", decodeOutput(t, outputs[0]))
	sheet := image.NewNRGBA(image.Rect(0, 0, 760, 376))
	for y := 0; y < sheet.Bounds().Dy(); y++ {
		for x := 0; x < sheet.Bounds().Dx(); x++ {
			shade := uint8(245)
			if (x/12+y/12)%2 != 0 {
				shade = 210
			}
			sheet.SetNRGBA(x, y, color.NRGBA{shade, shade, shade, 255})
		}
	}
	for _, panel := range []struct {
		name string
		at   image.Point
	}{
		{"orientation.png", image.Pt(16, 16)},
		{"transparency.png", image.Pt(208, 16)},
		{"transparency-avif-64.png", image.Pt(480, 16)},
		{"color-linear-before.png", image.Pt(208, 232)},
		{"color-srgb.png", image.Pt(480, 232)},
	} {
		data, err := os.ReadFile(filepath.Join(dir, panel.name))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		draw.Draw(sheet, img.Bounds().Add(panel.at), img, img.Bounds().Min, draw.Over)
	}
	encoded.Reset()
	if err = png.Encode(&encoded, sheet); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "review-sheet.png", encoded.Bytes())
	t.Logf("preserved visual review PNGs in %s", dir)
}
