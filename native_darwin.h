// macOS calls behind the local target: the session, windows, shots, frames and input.
// Coordinates are points from the top left of the main display.
#include <stdbool.h>
#include <stdint.h>

void dr_init_app(void);

bool dr_accessibility(bool prompt);
bool dr_screen_recording(bool request);
bool dr_input_monitoring(bool request);
// Camera (0) and microphone (1): 3 is allowed. Asking waits for the answer.
int dr_media_status(int kind);
void dr_media_request(int kind);
// Bluetooth: 3 is allowed. Asking waits up to the timeout for an answer.
int dr_bluetooth_status(void);
void dr_bluetooth_request(double timeout);

// The console session: user name, whether it's on the console, whether the screen is locked.
void dr_session(char **user, bool *on_console, bool *locked);
void dr_main_display(double *width, double *height);

// On-screen windows, front to back, one per line: owner, pid, layer, x, y, w, h, title.
char *dr_windows(bool all);

// A PNG of one app's largest window (by name, or pid when pid > 0), or the whole display when
// both are empty. Returns NULL with *err set on failure.
void *dr_shot(const char *app, int pid, long *size, char **err);

int dr_raise(int pid);

// Closes the notification banners on screen; returns how many it closed, -1 when Notification
// Center isn't running.
int dr_dismiss_notifications(void);

void dr_pointer(double *x, double *y);
void dr_mouse(int type, double x, double y, int clicks, int dx, int dy);
void dr_key(int code, uint64_t flags, bool down, int unicode);
void dr_scroll(double x, double y, int delta, int phase, int momentum);

// Keeps the latest frame of one window in a mapped file (frames.go has the format). When
// screen_ratio > 0 only the bottom of the window, width x width*screen_ratio, is kept: a VM's
// screen under its title bar. Returns 0, or -1 with *err set.
int dr_watch(int pid, const char *path, double screen_ratio, double *points_wide, double *points_high, char **err);
