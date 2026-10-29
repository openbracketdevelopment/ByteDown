package processing

/*
#cgo pkg-config: vips lcms2
#include "native.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/davidbyttow/govips/v2/vips"
)

func normalize(data []byte, format string) (*vips.ImageRef, error) {
	if err := startup(); err != nil {
		return nil, err
	}
	var result C.bd_image
	defer C.bd_image_free(&result)
	jpeg := C.int(0)
	if format == "jpeg" {
		jpeg = 1
	}
	status := C.bd_normalize(unsafe.Pointer(&data[0]), C.size_t(len(data)), jpeg, &result)
	runtime.KeepAlive(data)
	if status != 0 {
		return nil, fmt.Errorf("normalize image: %s", C.GoString(result.error))
	}
	if result.length > 1<<31-1 {
		return nil, fmt.Errorf("normalized raster is too large")
	}
	pixels := C.GoBytes(result.pixels, C.int(result.length))
	return vips.NewImageFromMemory(pixels, int(result.width), int(result.height),
		int(result.bands), vips.BandFormatUchar, vips.InterpretationSRGB)
}
