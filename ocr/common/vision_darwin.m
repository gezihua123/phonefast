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

        // Run Vision text detection: reportCharacterBoxes=YES gives
        // character-level boxes which we merge on the Go side into
        // word/line-level boxes. This catches more text than the
        // default line-level detection alone.
        VNDetectTextRectanglesRequest *req = [[VNDetectTextRectanglesRequest alloc] init];
        req.reportCharacterBoxes = YES;

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

        // Count total character boxes (if reportCharacterBoxes=YES) plus
        // fallback top-level boxes (for text regions with no char details).
        NSUInteger totalRegions = 0;
        for (NSUInteger i = 0; i < results.count; i++) {
            VNTextObservation *obs = results[i];
            NSArray<VNRectangleObservation *> *chars = obs.characterBoxes;
            if (chars && chars.count > 0) {
                totalRegions += chars.count;
            } else {
                totalRegions += 1; // fallback to top-level box
            }
        }

        TextRegion *regions = (TextRegion *)malloc(totalRegions * sizeof(TextRegion));
        if (!regions) { CGImageRelease(cgImage); return NULL; }

        NSUInteger ri = 0;
        for (NSUInteger i = 0; i < results.count; i++) {
            VNTextObservation *obs = results[i];
            NSArray<VNRectangleObservation *> *chars = obs.characterBoxes;

            if (chars && chars.count > 0) {
                for (VNRectangleObservation *ch in chars) {
                    CGRect bb = ch.boundingBox;
                    CGFloat pixelY = (1.0 - bb.origin.y - bb.size.height) * height;
                    if (pixelY < 0) pixelY = 0;
                    regions[ri].x = bb.origin.x * width;
                    regions[ri].y = pixelY;
                    regions[ri].width = bb.size.width * width;
                    regions[ri].height = bb.size.height * height;
                    ri++;
                }
            } else {
                CGRect bb = obs.boundingBox;
                CGFloat pixelY = (1.0 - bb.origin.y - bb.size.height) * height;
                if (pixelY < 0) pixelY = 0;
                regions[ri].x = bb.origin.x * width;
                regions[ri].y = pixelY;
                regions[ri].width = bb.size.width * width;
                regions[ri].height = bb.size.height * height;
                ri++;
            }
        }

        *outCount = (int)ri;
        CGImageRelease(cgImage);
        return regions;
    }
}

// freeTextRegions releases the memory allocated by detectTextRegions.
void freeTextRegions(TextRegion *regions) {
    free(regions);
}
