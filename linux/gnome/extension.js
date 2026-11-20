// deskrig's helper inside GNOME Shell, on Wayland and on Xorg. GNOME gives no other program the
// window list, a way to raise a window, or shots, so deskrig asks this extension over D-Bus:
// org.gnome.Shell, /io/github/sudiptadeb/Deskrig. deskrig installs it; GNOME loads it at login.
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Shell from 'gi://Shell';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

Gio._promisify(Shell.Screenshot, 'composite_to_stream');
Gio._promisify(Shell.Screenshot.prototype, 'screenshot');

// deskrig checks this, and says to log in again when it has installed a newer one.
const VERSION = 1;

const IFACE = `<node><interface name="io.github.sudiptadeb.Deskrig">
  <method name="Version"><arg type="u" direction="out" name="version"/></method>
  <method name="Screen"><arg type="i" direction="out" name="width"/><arg type="i" direction="out" name="height"/></method>
  <method name="Windows"><arg type="s" direction="out" name="json"/></method>
  <method name="Raise"><arg type="t" direction="in" name="id"/><arg type="b" direction="out" name="ok"/></method>
  <method name="Shot"><arg type="t" direction="in" name="id"/><arg type="s" direction="in" name="path"/>
    <arg type="i" direction="out" name="width"/><arg type="i" direction="out" name="height"/></method>
  <method name="DismissNotifications"><arg type="i" direction="out" name="closed"/></method>
</interface></node>`;

function frontToBack() {
    const ws = global.get_window_actors().map(a => a.meta_window);
    return global.display.sort_windows_by_stacking(ws).reverse();
}

function find(id) {
    return frontToBack().find(w => w.get_id() === Number(id));
}

export default class DeskrigExtension extends Extension {
    enable() {
        this._dbus = Gio.DBusExportedObject.wrapJSObject(IFACE, this);
        this._dbus.export(Gio.DBus.session, '/io/github/sudiptadeb/Deskrig');
    }

    disable() {
        this._dbus.unexport();
        this._dbus = null;
    }

    Version() {
        return VERSION;
    }

    Screen() {
        return [global.screen_width, global.screen_height];
    }

    Windows() {
        const tracker = Shell.WindowTracker.get_default();
        return JSON.stringify(frontToBack().map(w => {
            const r = w.get_frame_rect();
            const app = tracker.get_window_app(w);
            return {
                id: w.get_id(), pid: w.get_pid(), app: app ? app.get_name() : (w.get_wm_class() || ''),
                appId: app ? (app.get_id() || '').replace(/\.desktop$/, '') : '', title: w.get_title() || '',
                x: r.x, y: r.y, w: r.width, h: r.height, type: w.get_window_type(),
                minimized: w.minimized, focused: w.has_focus(),
            };
        }));
    }

    Raise(id) {
        const w = find(id);
        if (!w)
            return false;
        Main.activateWindow(w);
        return true;
    }

    // A window as it draws itself, even when covered (Mutter keeps every window's own image),
    // cut to its frame without the shadow; id 0 is the whole screen.
    async ShotAsync([id, path], invocation) {
        try {
            const stream = Gio.File.new_for_path(path).replace(null, false, Gio.FileCreateFlags.NONE, null);
            let width, height;
            if (Number(id) === 0) {
                await new Shell.Screenshot().screenshot(false, stream);
                [width, height] = [global.screen_width, global.screen_height];
            } else {
                const w = find(id);
                if (!w)
                    throw new Error(`no window ${id}`);
                const texture = w.get_compositor_private().paint_to_content(null).get_texture();
                const frame = w.get_frame_rect(), buffer = w.get_buffer_rect();
                const scale = texture.get_width() / buffer.width;
                await Shell.Screenshot.composite_to_stream(texture,
                    (frame.x - buffer.x) * scale, (frame.y - buffer.y) * scale,
                    frame.width * scale, frame.height * scale, 1, null, 0, 0, 1, stream);
                [width, height] = [Math.round(frame.width * scale), Math.round(frame.height * scale)];
            }
            stream.close(null);
            invocation.return_value(new GLib.Variant('(ii)', [width, height]));
        } catch (e) {
            invocation.return_error_literal(Gio.DBusError, Gio.DBusError.FAILED, String(e));
        }
    }

    DismissNotifications() {
        let closed = 0;
        for (const source of Main.messageTray.getSources()) {
            closed += source.notifications.length;
            source.destroy();
        }
        return closed;
    }
}
