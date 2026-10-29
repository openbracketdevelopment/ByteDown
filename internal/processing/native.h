#ifndef BYTEDOWN_PROCESSING_NATIVE_H
#define BYTEDOWN_PROCESSING_NATIVE_H
#include <stddef.h>

typedef struct {
    void *pixels;
    size_t length;
    int width;
    int height;
    int bands;
    char *error;
} bd_image;

int bd_normalize(const void *data, size_t length, int jpeg, bd_image *result);
void bd_image_free(bd_image *result);
#endif
