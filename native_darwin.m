#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreBluetooth/CoreBluetooth.h>
#import <CoreMedia/CoreMedia.h>
#import <ImageIO/ImageIO.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#include <mach/mach_time.h>
#include <stdatomic.h>
#include <sys/mman.h>
#include <fcntl.h>
#include <unistd.h>
#include "native_darwin.h"

static char *copy(NSString *s) { return strdup(s ? s.UTF8String : ""); }

// ScreenCaptureKit needs a connection to the window server, which the shared application makes.
// Waits for an answer from a system framework while running this thread's run loop: some
// (ScreenCaptureKit among them) can answer on the main queue, and deskrig's main thread is the
// one waiting, so blocking it outright can wait forever. Returns false on timeout (seconds; 0 is none).
static bool waitFor(dispatch_semaphore_t done, double seconds) {
    NSDate *end = seconds > 0 ? [NSDate dateWithTimeIntervalSinceNow:seconds] : NSDate.distantFuture;
    while (dispatch_semaphore_wait(done, DISPATCH_TIME_NOW) != 0) {
        if ([end timeIntervalSinceNow] <= 0) return false;
        [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
    }
    return true;
}

// Runs this thread's run loop for a while, so the main queue's work gets done.
void dr_run_loop(double seconds) {
    [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:seconds]];
}

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
    waitFor(done, 0);
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
    waitFor(done, 0);
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
        waitFor(done, 0);
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

// Brings an app's windows back from the Dock or another Space: un-minimises them, raises its
// main window and makes it frontmost, which moves the desktop to the window. -1 when the app
// has no windows.
int dr_show(int pid) {
    AXUIElementRef app = AXUIElementCreateApplication(pid);
    CFTypeRef windows = NULL;
    AXError e = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &windows);
    if (e != kAXErrorSuccess || !windows || CFArrayGetCount(windows) == 0) {
        if (windows) CFRelease(windows);
        CFRelease(app);
        return e != kAXErrorSuccess ? e : -1;
    }
    for (id w in (__bridge NSArray *)windows)
        AXUIElementSetAttributeValue((__bridge AXUIElementRef)w, kAXMinimizedAttribute, kCFBooleanFalse);
    CFTypeRef main = NULL;
    AXUIElementRef front = (AXUIElementCopyAttributeValue(app, kAXMainWindowAttribute, &main) == kAXErrorSuccess && main)
        ? (AXUIElementRef)main : (__bridge AXUIElementRef)((__bridge NSArray *)windows).firstObject;
    AXUIElementPerformAction(front, kAXRaiseAction);
    e = AXUIElementSetAttributeValue(app, kAXFrontmostAttribute, kCFBooleanTrue);
    [[NSRunningApplication runningApplicationWithProcessIdentifier:pid] activateWithOptions:0];
    if (main) CFRelease(main);
    CFRelease(windows);
    CFRelease(app);
    return e;
}

// The pid of a running app with this name, or 0.
int dr_app_pid(const char *name) {
    for (NSRunningApplication *a in NSWorkspace.sharedWorkspace.runningApplications)
        if ([a.localizedName isEqualToString:@(name)]) return a.processIdentifier;
    return 0;
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
    // No modifiers: a source-less event takes whatever the session last saw, and a Ctrl left
    // over from a key combination turns a click into a right click.
    CGEventSetFlags(e, 0);
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

// A modifier key going down or up, as a keyboard sends it; flags are those held afterwards.
void dr_modifier(int code, uint64_t flags) {
    CGEventRef e = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)code, true);
    CGEventSetType(e, kCGEventFlagsChanged);
    CGEventSetFlags(e, (CGEventFlags)flags);
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
@property(strong) SCStream *stream;
@property(copy) NSString *failed;
@end

@implementation Watcher {
    uint8_t *map;
    size_t size;
    uint64_t count;
    uint32_t hidden;
}

- (void)setHidden:(uint32_t)h {
    hidden = h;
    if (map) memcpy(map + 28, &hidden, 4);
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
    memcpy(map + 8, &count, 8);
    memcpy(map + 28, &hidden, 4);
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
    self.failed = error.localizedDescription;
}
@end

// Watches by number; one process (the capture server) holds them all, since macOS lets only
// one process at deskrig's path capture at a time.
static NSMutableDictionary<NSNumber *, Watcher *> *watchers;
static int lastWatch;

static void stopStream(SCStream *stream) {
    if (!stream) return;
    dispatch_semaphore_t stopped = dispatch_semaphore_create(0);
    [stream stopCaptureWithCompletionHandler:^(NSError *e) { dispatch_semaphore_signal(stopped); }];
    waitFor(stopped, 2);
}

// Watches pid's window into path: a new watch when id is 0, or watches afresh into watch id's
// file (the window came back), keeping its count. Returns the watch's id, or -1 with *err set.
int dr_watch(int id, int pid, const char *path, double screen_ratio, double *points_wide, double *points_high, char **err) {
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
    if (!watchers) watchers = [NSMutableDictionary dictionary];
    Watcher *watcher = id ? watchers[@(id)] : nil;
    if (watcher) {
        stopStream(watcher.stream);
        watcher.stream = nil;
    } else {
        watcher = [Watcher new];
        watcher.path = @(path);
        id = ++lastWatch;
    }
    SCStream *stream = [[SCStream alloc] initWithFilter:filter configuration:config delegate:watcher];
    NSError *e = nil;
    if (![stream addStreamOutput:watcher type:SCStreamOutputTypeScreen
              sampleHandlerQueue:dispatch_queue_create("frames", DISPATCH_QUEUE_SERIAL) error:&e]) {
        *err = copy(e.localizedDescription);
        return -1;
    }
    __block NSError *failed = nil;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    [stream startCaptureWithCompletionHandler:^(NSError *e) {
        failed = e;
        dispatch_semaphore_signal(done);
    }];
    waitFor(done, 0);
    if (failed) { *err = copy(failed.localizedDescription); return -1; }
    watcher.stream = stream;
    watcher.failed = nil;
    watchers[@(id)] = watcher;
    *points_wide = source.size.width;
    *points_high = source.size.height;
    return id;
}

void dr_unwatch(int id) {
    stopStream(watchers[@(id)].stream);
    [watchers removeObjectForKey:@(id)];
}

void dr_watch_hidden(int id, int hidden) { [watchers[@(id)] setHidden:hidden]; }

// Why watch id's stream stopped by itself, or NULL while it runs.
char *dr_watch_failed(int id) {
    NSString *f = watchers[@(id)].failed;
    return f ? copy(f) : NULL;
}

// Recording: every frame the screen (or one window) shows, as PNGs named by the UTC time they
// were on screen, with a line per frame in frames.tsv: number, that time, width, height, file. Frames are encoded on all cores and listed once their file is
// whole, so lines can arrive late and out of order; the times are when the frame was on screen.
// A time as UTC to the microsecond: 2026-11-12T16:45:05.123456Z, or for a file name
// 20261112T164505.123456Z.
static NSString *utc(double unix, bool forFile) {
    time_t secs = (time_t)floor(unix);
    int micros = (int)llround((unix - floor(unix)) * 1e6);
    if (micros == 1000000) { secs++; micros = 0; }
    struct tm t;
    gmtime_r(&secs, &t);
    char out[40];
    strftime(out, sizeof out, forFile ? "%Y%m%dT%H%M%S" : "%Y-%m-%dT%H:%M:%S", &t);
    return [NSString stringWithFormat:@"%s.%06dZ", out, micros];
}

@interface Recorder : NSObject <SCStreamOutput, SCStreamDelegate>
@property(copy) NSString *dir;
@end

static const long recordBacklog = 240;  // frames waiting to be encoded, about 1.2 GB at 1440x900

@implementation Recorder {
    FILE *list;
    long frames;
    double unixMinusMach;  // seconds
    mach_timebase_info_data_t timebase;
    dispatch_queue_t encoders, lister;
    dispatch_group_t pending;
    dispatch_semaphore_t room;
}

- (BOOL)open:(char **)err {
    list = fopen([self.dir stringByAppendingPathComponent:@"frames.tsv"].UTF8String, "w");
    if (!list) { *err = copy([NSString stringWithFormat:@"can't write in %@", self.dir]); return NO; }
    fprintf(list, "frame\ttime\twidth\theight\tfile\n");
    fflush(list);
    mach_timebase_info(&timebase);
    unixMinusMach = [NSDate date].timeIntervalSince1970 -
        (double)mach_absolute_time() * timebase.numer / timebase.denom / 1e9;
    encoders = dispatch_queue_create("encode", DISPATCH_QUEUE_CONCURRENT);
    lister = dispatch_queue_create("list", DISPATCH_QUEUE_SERIAL);
    pending = dispatch_group_create();
    room = dispatch_semaphore_create(recordBacklog);
    return YES;
}

- (void)stream:(SCStream *)stream didOutputSampleBuffer:(CMSampleBufferRef)buffer ofType:(SCStreamOutputType)type {
    if (type != SCStreamOutputTypeScreen || !CMSampleBufferIsValid(buffer)) return;
    NSDictionary *info = ((__bridge NSArray *)CMSampleBufferGetSampleAttachmentsArray(buffer, false)).firstObject;
    NSNumber *status = info[SCStreamFrameInfoStatus];
    if (!status || status.integerValue != SCFrameStatusComplete) return;
    CVPixelBufferRef pixels = CMSampleBufferGetImageBuffer(buffer);
    if (!pixels) return;
    NSNumber *shown = info[SCStreamFrameInfoDisplayTime];
    double when = shown ? unixMinusMach + shown.doubleValue * timebase.numer / timebase.denom / 1e9
                        : [NSDate date].timeIntervalSince1970;
    long n = ++frames;
    if (dispatch_semaphore_wait(room, DISPATCH_TIME_NOW) != 0) {
        dispatch_async(lister, ^{ fprintf(self->list, "%ld\t%s\t0\t0\t-\n", n, utc(when, false).UTF8String); fflush(self->list); });
        return;  // listed with no file: dropped, the encoders were too far behind
    }
    CVPixelBufferLockBaseAddress(pixels, kCVPixelBufferLock_ReadOnly);
    size_t w = CVPixelBufferGetWidth(pixels), h = CVPixelBufferGetHeight(pixels);
    size_t stride = CVPixelBufferGetBytesPerRow(pixels);
    NSData *copyOf = [NSData dataWithBytes:CVPixelBufferGetBaseAddress(pixels) length:stride * h];
    CVPixelBufferUnlockBaseAddress(pixels, kCVPixelBufferLock_ReadOnly);
    NSString *name = [utc(when, true) stringByAppendingString:@".png"];
    NSString *path = [self.dir stringByAppendingPathComponent:name];
    dispatch_group_async(pending, encoders, ^{
        CGDataProviderRef data = CGDataProviderCreateWithCFData((__bridge CFDataRef)copyOf);
        CGColorSpaceRef space = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
        CGImageRef image = CGImageCreate(w, h, 8, 32, stride, space,
            kCGBitmapByteOrder32Little | kCGImageAlphaNoneSkipFirst, data, NULL, false, kCGRenderingIntentDefault);
        NSString *partial = [path stringByAppendingString:@".part"];
        CGImageDestinationRef out = CGImageDestinationCreateWithURL(
            (__bridge CFURLRef)[NSURL fileURLWithPath:partial], (__bridge CFStringRef)UTTypePNG.identifier, 1, NULL);
        CGImageDestinationAddImage(out, image, NULL);
        bool ok = CGImageDestinationFinalize(out) && rename(partial.UTF8String, path.UTF8String) == 0;
        CFRelease(out);
        CGImageRelease(image);
        CGColorSpaceRelease(space);
        CGDataProviderRelease(data);
        dispatch_semaphore_signal(self->room);
        dispatch_group_async(self->pending, self->lister, ^{
            fprintf(self->list, "%ld\t%s\t%zu\t%zu\t%s\n", n, utc(when, false).UTF8String, ok ? w : 0, ok ? h : 0, ok ? name.UTF8String : "-");
            fflush(self->list);
        });
    });
}

- (long)finish {
    dispatch_group_wait(pending, DISPATCH_TIME_FOREVER);
    dispatch_sync(lister, ^{});
    fclose(list);
    return frames;
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
    fprintf(stderr, "the recording stopped: %s\n", error.localizedDescription.UTF8String);
}
@end

static Recorder *recorder;
static SCStream *recording;

// Records the main display, or app's frontmost window when app is not empty, into dir.
// Fills in the size it records, in points, and pixels per point.
int dr_record_start(const char *app, const char *dir, double *points_wide, double *points_high, double *scale, char **err) {
    SCShareableContent *c = content(err);
    if (!c) return -1;
    SCContentFilter *filter;
    CGSize size;
    if (app[0]) {
        SCWindow *w = findWindow(c, app, 0);
        if (!w) { *err = copy([NSString stringWithFormat:@"no window on screen for %s", app]); return -1; }
        filter = [[SCContentFilter alloc] initWithDesktopIndependentWindow:w];
        size = w.frame.size;
    } else {
        SCDisplay *display = c.displays.firstObject;
        if (!display) { *err = copy(@"no display"); return -1; }
        filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];
        size = CGSizeMake(display.width, display.height);
    }
    SCStreamConfiguration *config = [SCStreamConfiguration new];
    config.showsCursor = NO;
    config.width = size.width * filter.pointPixelScale;
    config.height = size.height * filter.pointPixelScale;
    config.pixelFormat = kCVPixelFormatType_32BGRA;
    config.minimumFrameInterval = CMTimeMake(1, 60);
    config.queueDepth = 8;
    recorder = [Recorder new];
    recorder.dir = @(dir);
    if (![recorder open:err]) return -1;
    recording = [[SCStream alloc] initWithFilter:filter configuration:config delegate:recorder];
    NSError *e = nil;
    if (![recording addStreamOutput:recorder type:SCStreamOutputTypeScreen
                 sampleHandlerQueue:dispatch_queue_create("record", DISPATCH_QUEUE_SERIAL) error:&e]) {
        *err = copy(e.localizedDescription);
        return -1;
    }
    __block NSError *failed = nil;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    [recording startCaptureWithCompletionHandler:^(NSError *e) {
        failed = e;
        dispatch_semaphore_signal(done);
    }];
    waitFor(done, 0);
    if (failed) { *err = copy(failed.localizedDescription); return -1; }
    *points_wide = size.width;
    *points_high = size.height;
    *scale = filter.pointPixelScale;
    return 0;
}

// Stops recording, waits for every frame to be written, and says how many there were.
long dr_record_stop(void) {
    stopStream(recording);
    recording = nil;
    long n = [recorder finish];
    recorder = nil;
    return n;
}
