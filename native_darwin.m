#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreBluetooth/CoreBluetooth.h>
#import <CoreMedia/CoreMedia.h>
#import <ImageIO/ImageIO.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <stdatomic.h>
#include <sys/mman.h>
#include <fcntl.h>
#include <unistd.h>
#include "native_darwin.h"

static char *copy(NSString *s) { return strdup(s ? s.UTF8String : ""); }

// ScreenCaptureKit needs a connection to the window server, which the shared application makes.
void dr_init_app(void) { [NSApplication sharedApplication]; }

bool dr_accessibility(bool prompt) {
    if (!prompt) return AXIsProcessTrusted();
    return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef) @{(__bridge id)kAXTrustedCheckOptionPrompt: @YES});
}

bool dr_screen_recording(bool request) {
    return request ? CGRequestScreenCaptureAccess() : CGPreflightScreenCaptureAccess();
}

bool dr_input_monitoring(bool request) {
    return request ? CGRequestListenEventAccess() : CGPreflightListenEventAccess();
}

static AVMediaType mediaType(int kind) { return kind == 0 ? AVMediaTypeVideo : AVMediaTypeAudio; }

int dr_media_status(int kind) { return (int)[AVCaptureDevice authorizationStatusForMediaType:mediaType(kind)]; }

void dr_media_request(int kind) {
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    [AVCaptureDevice requestAccessForMediaType:mediaType(kind) completionHandler:^(BOOL granted) {
        dispatch_semaphore_signal(done);
    }];
    dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
}

int dr_bluetooth_status(void) { return (int)CBManager.authorization; }

@interface BluetoothAsker : NSObject <CBCentralManagerDelegate>
@end
@implementation BluetoothAsker
- (void)centralManagerDidUpdateState:(CBCentralManager *)central {}
@end

void dr_bluetooth_request(double timeout) {
    BluetoothAsker *asker = [BluetoothAsker new];
    CBCentralManager *central = [[CBCentralManager alloc] initWithDelegate:asker
        queue:dispatch_queue_create("bluetooth", DISPATCH_QUEUE_SERIAL)];
    for (double waited = 0; waited < timeout && CBManager.authorization == CBManagerAuthorizationNotDetermined; waited += 0.5)
        usleep(500000);
    (void)central;
}

int dr_automation(bool ask) {
    NSAppleEventDescriptor *target = [NSAppleEventDescriptor descriptorWithBundleIdentifier:@"com.apple.systemevents"];
    return AEDeterminePermissionToAutomateTarget(target.aeDesc, typeWildCard, typeWildCard, ask);
}

void dr_session(char **user, bool *on_console, bool *locked) {
    NSDictionary *s = CFBridgingRelease(CGSessionCopyCurrentDictionary());
    *user = copy(s[(__bridge NSString *)kCGSessionUserNameKey] ?: @"none");
    *on_console = [s[(__bridge NSString *)kCGSessionOnConsoleKey] boolValue];
    *locked = [s[@"CGSSessionScreenIsLocked"] boolValue];
}

void dr_main_display(double *width, double *height) {
    CGRect r = CGDisplayBounds(CGMainDisplayID());
    *width = r.size.width;
    *height = r.size.height;
}

// Layer 0 is apps' windows; others are system windows, menus and notices, which can sit
// invisibly over an app and take its clicks.
char *dr_windows(bool all) {
    NSArray *list = CFBridgingRelease(CGWindowListCopyWindowInfo(
        kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
    NSMutableString *out = [NSMutableString string];
    for (NSDictionary *w in list) {
        int layer = [w[(__bridge NSString *)kCGWindowLayer] intValue];
        if (!all && layer != 0) continue;
        CGRect r;
        CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)w[(__bridge NSString *)kCGWindowBounds], &r);
        NSString *title = [[w[(__bridge NSString *)kCGWindowName] ?: @"" componentsSeparatedByCharactersInSet:
            [NSCharacterSet newlineCharacterSet]] componentsJoinedByString:@" "];
        [out appendFormat:@"%@\t%d\t%d\t%d\t%d\t%d\t%d\t%@\n", w[(__bridge NSString *)kCGWindowOwnerName] ?: @"?",
            [w[(__bridge NSString *)kCGWindowOwnerPID] intValue], layer, (int)r.origin.x, (int)r.origin.y,
            (int)r.size.width, (int)r.size.height, title];
    }
    return copy(out);
}

static SCShareableContent *content(char **err) {
    __block SCShareableContent *got = nil;
    __block NSError *failed = nil;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *c, NSError *e) {
        got = c;
        failed = e;
        dispatch_semaphore_signal(done);
    }];
    dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
    if (!got) *err = copy([NSString stringWithFormat:@"capture failed: %@", failed.localizedDescription]);
    return got;
}

// The frontmost real window of an app, by name, or by pid when pid > 0.
static SCWindow *findWindow(SCShareableContent *c, const char *app, int pid) {
    NSString *name = app ? @(app) : @"";
    NSMutableDictionary<NSNumber *, SCWindow *> *byID = [NSMutableDictionary dictionary];
    for (SCWindow *w in c.windows) {
        if (!w.isOnScreen || w.windowLayer != 0) continue;
        if (pid > 0 ? w.owningApplication.processID != pid : ![w.owningApplication.applicationName isEqualToString:name])
            continue;
        byID[@(w.windowID)] = w;
    }
    // Front to back, skipping small helper windows (a toolbar strip, a tooltip) when there is
    // a real one.
    NSArray *order = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID));
    SCWindow *small = nil;
    for (NSDictionary *info in order) {
        SCWindow *w = byID[info[(__bridge NSString *)kCGWindowNumber]];
        if (!w) continue;
        if (w.frame.size.width >= 100 && w.frame.size.height >= 100) return w;
        if (!small) small = w;
    }
    return small ?: byID.allValues.firstObject;
}

void *dr_shot(const char *app, int pid, long *size, char **err) {
    @autoreleasepool {
        SCShareableContent *c = content(err);
        if (!c) return NULL;
        SCStreamConfiguration *config = [SCStreamConfiguration new];
        SCContentFilter *filter;
        bool whole = pid <= 0 && (!app || !*app);
        config.showsCursor = whole;
        if (whole) {
            SCDisplay *display = c.displays.firstObject;
            if (!display) { *err = copy(@"no display"); return NULL; }
            filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
            config.width = display.width * filter.pointPixelScale;
            config.height = display.height * filter.pointPixelScale;
        } else {
            SCWindow *w = findWindow(c, app, pid);
            if (!w) {
                *err = copy(pid > 0 ? [NSString stringWithFormat:@"no window on screen for pid %d", pid]
                                    : [NSString stringWithFormat:@"no window on screen for %s", app]);
                return NULL;
            }
            filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:w];
            config.width = w.frame.size.width * filter.pointPixelScale;
            config.height = w.frame.size.height * filter.pointPixelScale;
        }
        __block CGImageRef image = NULL;
        __block NSError *failed = nil;
        dispatch_semaphore_t done = dispatch_semaphore_create(0);
        [SCScreenshotManager captureImageWithFilter:filter configuration:config
                                  completionHandler:^(CGImageRef i, NSError *e) {
            if (i) image = CGImageRetain(i);
            failed = e;
            dispatch_semaphore_signal(done);
        }];
        dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
        if (!image) { *err = copy([NSString stringWithFormat:@"capture failed: %@", failed.localizedDescription]); return NULL; }
        NSMutableData *png = [NSMutableData data];
        CGImageDestinationRef dest = CGImageDestinationCreateWithData((__bridge CFMutableDataRef)png,
            (__bridge CFStringRef)UTTypePNG.identifier, 1, NULL);
        CGImageDestinationAddImage(dest, image, NULL);
        bool ok = CGImageDestinationFinalize(dest);
        CFRelease(dest);
        CGImageRelease(image);
        if (!ok) { *err = copy(@"can't make a PNG"); return NULL; }
        *size = png.length;
        void *bytes = malloc(png.length);
        memcpy(bytes, png.bytes, png.length);
        return bytes;
    }
}

int dr_raise(int pid) {
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    AXError e = AXUIElementSetAttributeValue(app, kAXFrontmostAttribute, kCFBooleanTrue);
    CFTypeRef window = NULL;
    if (AXUIElementCopyAttributeValue(app, kAXMainWindowAttribute, &window) == kAXErrorSuccess && window) {
        AXUIElementPerformAction(window, kAXRaiseAction);
        CFRelease(window);
    }
    CFRelease(app);
    return e;
}

// A banner offers "Close" among its actions, a stack "Clear All". Performing one changes the
// tree, so each pass stops at the first.
static bool closeOne(AXUIElementRef element, int depth) {
    if (depth > 12) return false;
    CFArrayRef names = NULL;
    if (AXUIElementCopyActionNames(element, &names) == kAXErrorSuccess && names) {
        for (NSString *name in (__bridge NSArray *)names) {
            if ([name containsString:@"Clear All"] || [name hasPrefix:@"Name:Close"] || [name isEqualToString:@"Close"]) {
                AXError e = AXUIElementPerformAction(element, (__bridge CFStringRef)name);
                CFRelease(names);
                return e == kAXErrorSuccess;
            }
        }
        CFRelease(names);
    }
    CFTypeRef children = NULL;
    if (AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, &children) != kAXErrorSuccess || !children) return false;
    bool closed = false;
    for (id child in (__bridge NSArray *)children) {
        if (closeOne((__bridge AXUIElementRef)child, depth + 1)) { closed = true; break; }
    }
    CFRelease(children);
    return closed;
}

int dr_dismiss_notifications(void) {
    NSRunningApplication *nc = [NSRunningApplication
        runningApplicationsWithBundleIdentifier:@"com.apple.notificationcenterui"].firstObject;
    if (!nc) return -1;
    AXUIElementRef app = AXUIElementCreateApplication(nc.processIdentifier);
    int closed = 0;
    while (closed < 50 && closeOne(app, 0)) {
        closed++;
        usleep(300000);
    }
    CFRelease(app);
    return closed;
}

void dr_pointer(double *x, double *y) {
    CGEventRef e = CGEventCreate(NULL);
    CGPoint p = CGEventGetLocation(e);
    CFRelease(e);
    *x = p.x;
    *y = p.y;
}

// The movement since the last event goes with it: the window server moves a dragged window by it.
void dr_mouse(int type, double x, double y, int clicks, int dx, int dy) {
    CGEventRef e = CGEventCreateMouseEvent(NULL, (CGEventType)type, CGPointMake(x, y), kCGMouseButtonLeft);
    CGEventSetIntegerValueField(e, kCGMouseEventClickState, clicks);
    CGEventSetIntegerValueField(e, kCGMouseEventDeltaX, dx);
    CGEventSetIntegerValueField(e, kCGMouseEventDeltaY, dy);
    CGEventPost(kCGHIDEventTap, e);
    CFRelease(e);
}

void dr_key(int code, uint64_t flags, bool down, int unicode) {
    CGEventRef e = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)code, down);
    CGEventSetFlags(e, (CGEventFlags)flags);
    if (unicode >= 0) {
        UniChar u = (UniChar)unicode;
        CGEventKeyboardSetUnicodeString(e, 1, &u);
    }
    CGEventPost(kCGHIDEventTap, e);
    CFRelease(e);
}

void dr_scroll(double x, double y, int delta, int phase, int momentum) {
    CGEventRef e = CGEventCreateScrollWheelEvent2(NULL, kCGScrollEventUnitPixel, 1, delta, 0, 0);
    CGEventSetLocation(e, CGPointMake(x, y));
    CGEventSetIntegerValueField(e, kCGScrollWheelEventIsContinuous, 1);
    CGEventSetIntegerValueField(e, kCGScrollWheelEventScrollPhase, phase);
    CGEventSetIntegerValueField(e, kCGScrollWheelEventMomentumPhase, momentum);
    CGEventPost(kCGHIDEventTap, e);
    CFRelease(e);
}

// The frame file, written as frames.go describes.
@interface Watcher : NSObject <SCStreamOutput, SCStreamDelegate>
@property(copy) NSString *path;
@end

@implementation Watcher {
    uint8_t *map;
    size_t size;
    uint64_t count;
}

- (void)resize:(size_t)bytes {
    if (map) munmap(map, size);
    map = NULL;
    int fd = open(self.path.UTF8String, O_RDWR | O_CREAT, 0600);
    if (fd < 0 || ftruncate(fd, bytes) != 0) { fprintf(stderr, "can't write %s\n", self.path.UTF8String); exit(1); }
    void *m = mmap(NULL, bytes, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
    close(fd);
    if (m == MAP_FAILED) { fprintf(stderr, "can't map %s\n", self.path.UTF8String); exit(1); }
    map = m;
    size = bytes;
    memcpy(map, "DESKRIG1", 8);
}

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)buffer ofType:(SCStreamOutputType)type {
    if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(buffer)) return;
    NSArray *infos = (__bridge NSArray *)CMSampleBufferGetSampleAttachmentsArray(buffer, false);
    NSNumber *status = infos.firstObject[SCStreamFrameInfoStatus];
    if (!status || status.integerValue != SCFrameStatusComplete) return;
    CVPixelBufferRef pixels = CMSampleBufferGetImageBuffer(buffer);
    if (!pixels) return;
    CVPixelBufferLockBaseAddress(pixels, kCVPixelBufferLock_ReadOnly);
    size_t w = CVPixelBufferGetWidth(pixels), h = CVPixelBufferGetHeight(pixels);
    size_t stride = CVPixelBufferGetBytesPerRow(pixels);
    if (size != 40 + stride * h) [self resize:40 + stride * h];
    uint8_t *base = CVPixelBufferGetBaseAddress(pixels);
    count++;
    memcpy(map + 8, &count, 8);  // odd: being written
    atomic_thread_fence(memory_order_seq_cst);
    memcpy(map + 40, base, stride * h);
    uint32_t dims[3] = {(uint32_t)w, (uint32_t)h, (uint32_t)stride};
    memcpy(map + 16, dims, 12);
    double now = [NSDate date].timeIntervalSince1970;
    memcpy(map + 32, &now, 8);
    atomic_thread_fence(memory_order_seq_cst);
    count++;
    memcpy(map + 8, &count, 8);  // even: whole
    CVPixelBufferUnlockBaseAddress(pixels, kCVPixelBufferLock_ReadOnly);
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
    fprintf(stderr, "the capture stopped: %s\n", error.localizedDescription.UTF8String);
    exit(1);
}
@end

static Watcher *watcher;
static SCStream *watching;

int dr_watch(int pid, const char *path, double screen_ratio, double *points_wide, double *points_high, char **err) {
    SCShareableContent *c = content(err);
    if (!c) return -1;
    SCWindow *w = findWindow(c, NULL, pid);
    if (!w) { *err = copy([NSString stringWithFormat:@"no window on screen for pid %d: is it minimised, or on another desktop (Space)?", pid]); return -1; }
    SCContentFilter *filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:w];
    SCStreamConfiguration *config = [SCStreamConfiguration new];
    CGSize s = w.frame.size;
    CGRect source = CGRectMake(0, 0, s.width, s.height);
    if (screen_ratio > 0) {
        double high = round(s.width * screen_ratio);
        source = CGRectMake(0, s.height - high, s.width, high);
        config.sourceRect = source;
    }
    config.showsCursor = NO;
    config.width = source.size.width * filter.pointPixelScale;
    config.height = source.size.height * filter.pointPixelScale;
    config.pixelFormat = kCVPixelFormatType_32BGRA;
    config.minimumFrameInterval = CMTimeMake(1, 60);
    config.queueDepth = 4;
    watcher = [Watcher new];
    watcher.path = @(path);
    watching = [[SCStream alloc] initWithFilter:filter configuration:config delegate:watcher];
    NSError *e = nil;
    if (![watching addStreamOutput:watcher type:SCStreamOutputTypeScreen
                sampleHandlerQueue:dispatch_queue_create("frames", DISPATCH_QUEUE_SERIAL) error:&e]) {
        *err = copy(e.localizedDescription);
        return -1;
    }
    __block NSError *failed = nil;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    [watching startCaptureWithCompletionHandler:^(NSError *e) {
        failed = e;
        dispatch_semaphore_signal(done);
    }];
    dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
    if (failed) { *err = copy(failed.localizedDescription); return -1; }
    *points_wide = source.size.width;
    *points_high = source.size.height;
    return 0;
}
