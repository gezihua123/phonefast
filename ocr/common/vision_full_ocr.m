//go:build darwin && cgo
// +build darwin,cgo

// vision_full_ocr.m — Apple Vision VNRecognizeTextRequest bridge.
// Returns recognized text + bounding boxes in one ANE-accelerated call.
// Supports Chinese (zh-Hans/zh-Hant) + English.

#import <Foundation/Foundation.h>
#import <Vision/Vision.h>
#import <CoreGraphics/CoreGraphics.h>
#import <AppKit/AppKit.h>

typedef struct {
    char *text;     // UTF-8 recognized text (caller must free() each)
    float x, y, width, height;  // pixel coordinates, top-left origin
    float confidence;
} RecognizedText;

// recognizeText runs VNRecognizeTextRequest on image bytes (PNG/JPEG).
// Returns malloc'd array of RecognizedText, count in *outCount.
// Caller calls freeRecognizedText() to release.
RecognizedText* recognizeText(const uint8_t *imgData, size_t imgLen,
                               int *outCount, const char *langs) {
    *outCount = 0;
    @autoreleasepool {
        NSData *data = [NSData dataWithBytes:imgData length:imgLen];
        if (!data) return NULL;

        CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)data, NULL);
        if (!source) return NULL;
        CGImageRef cgImage = CGImageSourceCreateImageAtIndex(source, 0, NULL);
        CFRelease(source);
        if (!cgImage) return NULL;

        size_t width = CGImageGetWidth(cgImage);
        size_t height = CGImageGetHeight(cgImage);

        VNRecognizeTextRequest *req = [[VNRecognizeTextRequest alloc] init];
        req.revision = VNRecognizeTextRequestRevision3;
        req.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
        req.usesLanguageCorrection = YES;

        // Language config: parse comma-separated list, default zh-Hans+en-US
        if (langs && strlen(langs) > 0) {
            NSString *s = [NSString stringWithUTF8String:langs];
            req.recognitionLanguages = [s componentsSeparatedByString:@","];
        } else {
            req.recognitionLanguages = @[@"zh-Hans", @"zh-Hant", @"en-US"];
        }

        VNImageRequestHandler *handler = [[VNImageRequestHandler alloc]
            initWithCGImage:cgImage options:@{}];

        NSError *error = nil;
        [handler performRequests:@[req] error:&error];

        if (error) {
            CGImageRelease(cgImage);
            return NULL;
        }

        NSArray<VNRecognizedTextObservation *> *results = req.results;
        if (!results || results.count == 0) {
            CGImageRelease(cgImage);
            return NULL;
        }

        // Collect recognized text items (top candidate per observation)
        NSMutableArray *items = [NSMutableArray array];
        for (VNRecognizedTextObservation *obs in results) {
            VNRecognizedText *top = [obs topCandidates:1].firstObject;
            if (!top || top.string.length == 0) continue;

            CGRect bb = obs.boundingBox;
            // Flip Y-axis (Vision bottom-left → top-left)
            CGFloat pixelY = (1.0 - bb.origin.y - bb.size.height) * height;
            if (pixelY < 0) pixelY = 0;

            RecognizedText rt;
            const char *utf8 = [top.string UTF8String];
            rt.text = strdup(utf8 ? utf8 : "");
            rt.x = (float)(bb.origin.x * width);
            rt.y = (float)pixelY;
            rt.width = (float)(bb.size.width * width);
            rt.height = (float)(bb.size.height * height);
            rt.confidence = (float)top.confidence;

            NSValue *val = [NSValue valueWithBytes:&rt objCType:@encode(RecognizedText)];
            [items addObject:val];
        }

        if (items.count == 0) {
            CGImageRelease(cgImage);
            return NULL;
        }

        RecognizedText *out = (RecognizedText *)malloc(items.count * sizeof(RecognizedText));
        if (!out) { CGImageRelease(cgImage); return NULL; }

        for (NSUInteger i = 0; i < items.count; i++) {
            [[items objectAtIndex:i] getValue:&out[i]];
        }

        *outCount = (int)items.count;
        CGImageRelease(cgImage);
        return out;
    }
}

void freeRecognizedText(RecognizedText *items, int count) {
    if (!items) return;
    for (int i = 0; i < count; i++) {
        free(items[i].text);
    }
    free(items);
}
