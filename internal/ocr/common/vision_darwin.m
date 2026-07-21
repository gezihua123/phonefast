//go:build darwin && cgo
// +build darwin,cgo

#import <Foundation/Foundation.h>
#import <Vision/Vision.h>
#import <CoreGraphics/CoreGraphics.h>
#import <AppKit/AppKit.h>

// TextRegion represents one detected text bounding box in pixel coordinates.
typedef struct {
    float x, y, width, height;
} TextRegion;

// detectTextRegions runs VNDetectTextRectanglesRequest on PNG image bytes.
// Returns malloc'd array of TextRegion, count written to *outCount.
// Caller must free() the returned array.
TextRegion* detectTextRegions(const uint8_t *pngData, size_t pngLen, int *outCount) {
    *outCount = 0;

    @autoreleasepool {
        NSData *data = [NSData dataWithBytes:pngData length:pngLen];
        if (!data) return NULL;

        // Create image from PNG data
        CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)data, NULL);
        if (!source) return NULL;
        CGImageRef cgImage = CGImageSourceCreateImageAtIndex(source, 0, NULL);
        CFRelease(source);
        if (!cgImage) return NULL;

        size_t width = CGImageGetWidth(cgImage);
        size_t height = CGImageGetHeight(cgImage);

        // Run Vision text detection
        VNDetectTextRectanglesRequest *req = [[VNDetectTextRectanglesRequest alloc] init];
        req.reportCharacterBoxes = NO;

        VNImageRequestHandler *handler = [[VNImageRequestHandler alloc]
            initWithCGImage:cgImage options:@{}];

        NSError *error = nil;
        [handler performRequests:@[req] error:&error];

        if (error) {
            CGImageRelease(cgImage);
            return NULL;
        }

        NSArray<VNTextObservation *> *results = req.results;
        if (!results || results.count == 0) {
            CGImageRelease(cgImage);
            return NULL;
        }

        // Allocate result array
        TextRegion *regions = (TextRegion *)malloc(results.count * sizeof(TextRegion));
        if (!regions) {
            CGImageRelease(cgImage);
            return NULL;
        }

        // Convert Vision normalized coordinates to pixel coordinates
        // Vision uses bottom-left origin, we flip to top-left
        for (NSUInteger i = 0; i < results.count; i++) {
            VNTextObservation *obs = results[i];
            CGRect bb = obs.boundingBox;

            // Flip Y-axis (Vision uses bottom-left origin)
            CGFloat pixelY = (1.0 - bb.origin.y - bb.size.height) * height;
            if (pixelY < 0) pixelY = 0;

            regions[i].x = bb.origin.x * width;
            regions[i].y = pixelY;
            regions[i].width = bb.size.width * width;
            regions[i].height = bb.size.height * height;
        }

        *outCount = (int)results.count;
        CGImageRelease(cgImage);
        return regions;
    }
}

// freeTextRegions releases the memory allocated by detectTextRegions.
void freeTextRegions(TextRegion *regions) {
    free(regions);
}
