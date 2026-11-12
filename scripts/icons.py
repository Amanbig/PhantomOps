#!/usr/bin/env python3
"""Regenerate every PhantomOps icon from the SVG sources.

    python3 scripts/icons.py

Sources:
  build/appicon.svg        detailed icon (48 px and up)
  build/appicon-small.svg  simplified icon for 32 px and below (bigger ghost,
                           no ring), which stays legible in task bars and tabs

Outputs:
  build/appicon.png                    1024 px (Linux window icon, macOS .icns)
  build/windows/icon.ico               16-256 px
  build/linux/icons/<N>x<N>.png        hicolor icons installed by the .deb/.rpm
  frontend/src/assets/images/logo.svg  title bar logo (small variant)

Rendering uses WebKitGTK through PyGObject (gir1.2-webkit2-4.1, already
needed to build the app on Linux) so the SVGs render exactly as in the app;
Pillow writes the .ico. Without a GPU, libEGL may print "failed to create
dri2 screen" warnings; they are harmless.
"""
import os
import shutil
import sys

import gi

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
gi.require_version("WebKit2", "4.1")
from gi.repository import Gdk, GLib, Gtk, WebKit2  # noqa: E402
from PIL import Image  # noqa: E402

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DETAILED = os.path.join(ROOT, "build", "appicon.svg")
SMALL = os.path.join(ROOT, "build", "appicon-small.svg")
SMALL_MAX = 32  # sizes <= this use the simplified artwork


def render(svg_path, size, out_path):
    """Rasterise svg_path at exactly size x size pixels with transparency."""
    svg = open(svg_path, encoding="utf-8").read()
    # Only the root <svg> carries width/height="1024"; resize just that one.
    svg = svg.replace('width="1024" height="1024"', f'width="{size}" height="{size}"', 1)
    html = f'<html><body style="margin:0;background:transparent;overflow:hidden">{svg}</body></html>'

    win = Gtk.OffscreenWindow()
    view = WebKit2.WebView()
    view.set_background_color(Gdk.RGBA(0, 0, 0, 0))
    view.set_size_request(size, size)
    win.add(view)
    win.show_all()
    result = {}

    def done(v, res):
        try:
            v.get_snapshot_finish(res).write_to_png(out_path)
            result["ok"] = True
        except Exception as exc:  # report, never hang
            result["error"] = exc
        Gtk.main_quit()

    def loaded(v, event):
        if event == WebKit2.LoadEvent.FINISHED:
            GLib.timeout_add(250, lambda: v.get_snapshot(
                WebKit2.SnapshotRegion.VISIBLE, WebKit2.SnapshotOptions.TRANSPARENT_BACKGROUND, None, done) and False)

    view.connect("load-changed", loaded)
    view.load_html(html, "file:///")
    GLib.timeout_add(20000, lambda: (result.setdefault("error", "timed out"), Gtk.main_quit()))
    Gtk.main()
    win.destroy()
    if "error" in result:
        sys.exit(f"rendering {os.path.basename(svg_path)} at {size}px failed: {result['error']}")
    im = Image.open(out_path)
    if im.size != (size, size):
        sys.exit(f"{out_path}: expected {size}x{size}, got {im.size}")
    return im


def source_for(size):
    return SMALL if size <= SMALL_MAX else DETAILED


def main():
    os.environ.setdefault("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
    tmp = os.path.join(ROOT, "build", ".icons-tmp")
    os.makedirs(tmp, exist_ok=True)
    try:
        sizes = sorted({16, 24, 32, 48, 64, 128, 256, 512, 1024})
        imgs = {s: render(source_for(s), s, os.path.join(tmp, f"{s}.png")) for s in sizes}

        shutil.copyfile(os.path.join(tmp, "1024.png"), os.path.join(ROOT, "build", "appicon.png"))

        ico_sizes = [16, 24, 32, 48, 64, 128, 256]
        base = imgs[256]
        base.save(os.path.join(ROOT, "build", "windows", "icon.ico"), format="ICO",
                  sizes=[(s, s) for s in ico_sizes],
                  append_images=[imgs[s] for s in ico_sizes if s != 256])

        linux_dir = os.path.join(ROOT, "build", "linux", "icons")
        os.makedirs(linux_dir, exist_ok=True)
        for s in (16, 24, 32, 48, 64, 128, 256, 512):
            shutil.copyfile(os.path.join(tmp, f"{s}.png"), os.path.join(linux_dir, f"{s}x{s}.png"))

        shutil.copyfile(SMALL, os.path.join(ROOT, "frontend", "src", "assets", "images", "logo.svg"))
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    print("icons regenerated")


if __name__ == "__main__":
    main()
