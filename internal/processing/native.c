#include "native.h"
#include <lcms2.h>
#include <vips/vips.h>
#include <stdint.h>
#include <string.h>

static int bd_profile(VipsImage *image) {
    const void *data = NULL;
    size_t length = 0;
    if (!vips_image_get_typeof(image, VIPS_META_ICC_NAME)) {
        if (image->Type == VIPS_INTERPRETATION_CMYK) {
            vips_error("processing", "%s", "CMYK requires an embedded ICC profile");
            return -1;
        }
        return 0;
    }
    if (vips_image_get_blob(image, VIPS_META_ICC_NAME, &data, &length))
        return -1;
    if (length < 128 || length > 4 * 1024 * 1024 ||
        memcmp((const char *)data + 36, "acsp", 4) != 0) {
        vips_error("processing", "%s", "invalid ICC profile");
        return -1;
    }
    cmsHPROFILE profile = cmsOpenProfileFromMem(data, (cmsUInt32Number)length);
    if (!profile) {
        vips_error("processing", "%s", "unreadable ICC profile");
        return -1;
    }
    cmsColorSpaceSignature space = cmsGetColorSpace(profile);
    cmsProfileClassSignature class = cmsGetDeviceClass(profile);
    cmsColorSpaceSignature pcs = cmsGetPCS(profile);
    int gray = image->Type == VIPS_INTERPRETATION_B_W ||
        image->Type == VIPS_INTERPRETATION_GREY16;
    int compatible = image->Type == VIPS_INTERPRETATION_CMYK
        ? space == cmsSigCmykData : gray
        ? space == cmsSigGrayData : space == cmsSigRgbData;
    int supported = class == cmsSigInputClass || class == cmsSigDisplayClass ||
        class == cmsSigOutputClass || class == cmsSigColorSpaceClass;
    supported = supported && (pcs == cmsSigXYZData || pcs == cmsSigLabData);
    supported = supported && cmsIsIntentSupported(profile, INTENT_PERCEPTUAL,
        LCMS_USED_AS_INPUT);
    cmsHPROFILE target = cmsCreate_sRGBProfile();
    cmsUInt32Number format = space == cmsSigCmykData ? TYPE_CMYK_8 :
        space == cmsSigGrayData ? TYPE_GRAY_8 : TYPE_RGB_8;
    cmsHTRANSFORM transform = compatible && supported && target
        ? cmsCreateTransform(profile, format, target, TYPE_RGB_8,
            INTENT_PERCEPTUAL, 0) : NULL;
    supported = supported && transform != NULL;
    if (transform)
        cmsDeleteTransform(transform);
    if (target)
        cmsCloseProfile(target);
    cmsCloseProfile(profile);
    if (!compatible || !supported) {
        vips_error("processing", "%s", "unsupported or incompatible ICC profile");
        return -1;
    }
    return 1;
}

int bd_normalize(const void *data, size_t length, int jpeg, bd_image *result) {
    VipsImage *loaded = NULL, *oriented = NULL, *managed = NULL;
    VipsImage *rgb = NULL, *bytes = NULL;
    int status = -1;
    memset(result, 0, sizeof(*result));
    vips_error_clear();
    /* govips' buffer FailOnError maps to deprecated "fail". Use the modern
       warning threshold explicitly so corrupt inputs never become padded images. */
    int failed = jpeg
        ? vips_jpegload_buffer((void *)data, length, &loaded,
            "fail_on", VIPS_FAIL_ON_WARNING, NULL)
        : vips_pngload_buffer((void *)data, length, &loaded,
            "fail_on", VIPS_FAIL_ON_WARNING, NULL);
    if (failed)
        goto done;
    VipsInterpretation type = loaded->Type;
    if (loaded->BandFmt != VIPS_FORMAT_UCHAR) {
        vips_error("processing", "%s", "non-8-bit decoded input is unsupported");
        goto done;
    }
    if (type != VIPS_INTERPRETATION_sRGB && type != VIPS_INTERPRETATION_RGB &&
        type != VIPS_INTERPRETATION_B_W && type != VIPS_INTERPRETATION_CMYK) {
        vips_error("processing", "%s", "unsupported input colorspace");
        goto done;
    }
    int channels = type == VIPS_INTERPRETATION_CMYK ? 4 :
        (type == VIPS_INTERPRETATION_B_W || type == VIPS_INTERPRETATION_GREY16) ? 1 : 3;
    if (loaded->Bands != channels && loaded->Bands != channels + 1) {
        vips_error("processing", "%s", "unsupported input channels");
        goto done;
    }
    int profile = bd_profile(loaded);
    if (profile < 0 || vips_autorot(loaded, &oriented, NULL))
        goto done;
    if (profile) {
        if (vips_icc_transform(oriented, &managed, "srgb", "embedded", TRUE,
            "depth", 8, "intent", VIPS_INTENT_PERCEPTUAL, NULL))
            goto done;
    } else {
        managed = g_object_ref(oriented);
    }
    if (vips_colourspace(managed, &rgb, VIPS_INTERPRETATION_sRGB, NULL) ||
        vips_cast(rgb, &bytes, VIPS_FORMAT_UCHAR, NULL))
        goto done;
    result->pixels = vips_image_write_to_memory(bytes, &result->length);
    if (!result->pixels)
        goto done;
    result->width = bytes->Xsize;
    result->height = bytes->Ysize;
    result->bands = bytes->Bands;
    status = 0;
done:
    if (status)
        result->error = g_strdup(vips_error_buffer());
    VIPS_UNREF(bytes);
    VIPS_UNREF(rgb);
    VIPS_UNREF(managed);
    VIPS_UNREF(oriented);
    VIPS_UNREF(loaded);
    vips_error_clear();
    return status;
}

void bd_image_free(bd_image *result) {
    g_free(result->pixels);
    g_free(result->error);
}
