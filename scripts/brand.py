"""The MoonPassword mark: a crescent moon with a keyhole cut out, in a 24x24 design space (docs/brand.md).

    python3 scripts/brand.py           writes preview PNGs to $TMPDIR (or the system temp folder)
    python3 scripts/brand.py --emit    also rewrites the icon files of all three apps

Needs Pillow.
"""
import math
import sys

from PIL import Image, ImageDraw

import os
import tempfile

OUT = tempfile.gettempdir() + os.sep
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__))).replace(os.sep, '/') + '/'

C1 = (12.0, 12.0, 9.0)    # the moon
C2 = (17.2, 8.0, 7.4)     # the shadow that carves the crescent
KH_C = (8.3, 13.2)        # keyhole: circle centre
KH_R = 1.75               # circle radius
KH_TOP_HALF = 0.85        # half width where the slot meets the circle
KH_BOTTOM_HALF = 1.45     # half width at the bottom
KH_BOTTOM = 17.6          # y of the slot's bottom edge

BG_TOP = (0x31, 0x2E, 0x81)    # indigo
BG_BOTTOM = (0x1E, 0x1B, 0x4B) # deep indigo
MOON = (0xF8, 0xF4, 0xE3)      # warm moonlight


def inside(c, p, margin=0.0):
    return math.hypot(p[0] - c[0], p[1] - c[1]) <= c[2] - margin


def intersections(a, b):
    (x0, y0, r0), (x1, y1, r1) = a, b
    d = math.hypot(x1 - x0, y1 - y0)
    l = (r0 * r0 - r1 * r1 + d * d) / (2 * d)
    h = math.sqrt(r0 * r0 - l * l)
    xm, ym = x0 + l * (x1 - x0) / d, y0 + l * (y1 - y0) / d
    return ((xm + h * (y1 - y0) / d, ym - h * (x1 - x0) / d), (xm - h * (y1 - y0) / d, ym + h * (x1 - x0) / d))


def arc(c, a0, a1, n):
    return [(c[0] + c[2] * math.cos(a0 + (a1 - a0) * i / n), c[1] + c[2] * math.sin(a0 + (a1 - a0) * i / n)) for i in range(n + 1)]


def ang(c, p):
    return math.atan2(p[1] - c[1], p[0] - c[0])


def crescent(n=72):
    p, q = intersections(C1, C2)
    # outer edge: along C1 from p to q the way that stays outside C2
    a0, a1 = ang(C1, p), ang(C1, q)
    for cand in (a1, a1 + 2 * math.pi, a1 - 2 * math.pi):
        mid = (a0 + cand) / 2
        if not inside(C2, (C1[0] + C1[2] * math.cos(mid), C1[1] + C1[2] * math.sin(mid))) and abs(cand - a0) < 2 * math.pi:
            a1 = cand
            break
    outer = arc(C1, a0, a1, n)
    # inner edge: along C2 from q back to p the way that stays inside C1
    b0, b1 = ang(C2, q), ang(C2, p)
    for cand in (b1, b1 + 2 * math.pi, b1 - 2 * math.pi):
        mid = (b0 + cand) / 2
        if inside(C1, (C2[0] + C2[2] * math.cos(mid), C2[1] + C2[2] * math.sin(mid))) and abs(cand - b0) < 2 * math.pi:
            b1 = cand
            break
    inner = arc(C2, b0, b1, n // 2)
    return outer + inner[1:-1]


def keyhole(n=40):
    cx, cy = KH_C
    # the circle, open at the bottom where the slot joins it
    t = math.asin(KH_TOP_HALF / KH_R)
    start, end = math.pi / 2 + t, math.pi / 2 - t + 2 * math.pi
    circle = [(cx + KH_R * math.cos(start + (end - start) * i / n), cy + KH_R * math.sin(start + (end - start) * i / n)) for i in range(n + 1)]
    return circle + [(cx + KH_BOTTOM_HALF, KH_BOTTOM), (cx - KH_BOTTOM_HALF, KH_BOTTOM)]


def check(cres, key):
    for p in key:
        assert inside(C1, p, 0.9) and not inside(C2, p, -0.9), ('keyhole too close to the edge', p)


def fmt(v):
    s = ('%.2f' % v).rstrip('0').rstrip('.')
    return s if s not in ('-0', '') else '0'


def path(polys, scale=1.0, dx=0.0, dy=0.0, sep=','):
    out = []
    for poly in polys:
        pts = [(x * scale + dx, y * scale + dy) for x, y in poly]
        out.append('M' + fmt(pts[0][0]) + sep + fmt(pts[0][1]) + ''.join('L' + fmt(x) + sep + fmt(y) for x, y in pts[1:]) + 'Z')
    return ''.join(out)


def render(size, polys, background, ss=8, pad=0.0, mono=False):
    big = size * ss
    if mono:
        img = Image.new('LA', (big, big), (0, 0))
    else:
        img = Image.new('RGBA', (big, big), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    if background:
        r = int(big * 0.225)
        grad = Image.new('RGBA', (big, big))
        gd = ImageDraw.Draw(grad)
        for y in range(big):
            t = y / big
            gd.line([(0, y), (big, y)], fill=tuple(int(BG_TOP[i] + (BG_BOTTOM[i] - BG_TOP[i]) * t) for i in range(3)) + (255,))
        mask = Image.new('L', (big, big), 0)
        ImageDraw.Draw(mask).rounded_rectangle([0, 0, big - 1, big - 1], r, fill=255)
        img.paste(grad, (0, 0), mask)
    scale = (big - 2 * pad * big) / 24.0
    off = pad * big
    cres, key = polys
    d.polygon([(x * scale + off, y * scale + off) for x, y in cres], fill=(0, 255) if mono else MOON + (255,))
    d.polygon([(x * scale + off, y * scale + off) for x, y in key], fill=(0, 0) if mono else ((0, 0, 0, 0) if not background else None))
    if background and not mono:
        # punch the keyhole through to the background gradient
        hole = Image.new('L', (big, big), 0)
        ImageDraw.Draw(hole).polygon([(x * scale + off, y * scale + off) for x, y in key], fill=255)
        grad = Image.new('RGBA', (big, big))
        gd = ImageDraw.Draw(grad)
        for y in range(big):
            t = y / big
            gd.line([(0, y), (big, y)], fill=tuple(int(BG_TOP[i] + (BG_BOTTOM[i] - BG_TOP[i]) * t) for i in range(3)) + (255,))
        img.paste(grad, (0, 0), hole)
    return img.resize((size, size), Image.LANCZOS)


cres, key = crescent(), keyhole()
check(cres, key)
# centre the mark's bounding box in the design space
xs, ys = [x for x, _ in cres], [y for _, y in cres]
dx, dy = 12 - (min(xs) + max(xs)) / 2, 12 - (min(ys) + max(ys)) / 2
cres = [(x + dx, y + dy) for x, y in cres]
key = [(x + dx, y + dy) for x, y in key]
render(256, (cres, key), True, pad=0.16).save(OUT + 'preview-icon.png')
render(44, (cres, key), False, mono=True, pad=0.04).save(OUT + 'preview-tray.png')
print('crescent points', len(cres), 'keyhole points', len(key))

if '--emit' in sys.argv:
    # macOS assets
    render(256, (cres, key), True, pad=0.16).save(ROOT + 'macos/MoonPassword.Mac/Assets/icon.png')
    render(44, (cres, key), False, mono=True, pad=0.04).save(ROOT + 'macos/MoonPassword.Mac/Assets/tray.png')
    # Android: adaptive foreground (108 viewport, mark inside the 66dp safe zone), monochrome status icon (24)
    s = 2.55
    fg = path([cres, key], s, 54 - 12 * s, 54 - 12 * s)
    open(ROOT + 'android/app/src/main/res/drawable/ic_launcher_foreground.xml', 'w', encoding='utf-8', newline='\n').write(
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<!-- The MoonPassword mark: a crescent moon with a keyhole cut out (even-odd fill). Generated, see docs/brand.md. -->\n'
        '<vector xmlns:android="http://schemas.android.com/apk/res/android"\n'
        '    android:width="108dp"\n    android:height="108dp"\n    android:viewportWidth="108"\n    android:viewportHeight="108">\n'
        '    <path\n        android:fillColor="#FFF8F4E3"\n        android:fillType="evenOdd"\n'
        '        android:pathData="' + fg + '" />\n</vector>\n')
    open(ROOT + 'android/app/src/main/res/drawable/ic_launcher_background.xml', 'w', encoding='utf-8', newline='\n').write(
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<vector xmlns:android="http://schemas.android.com/apk/res/android"\n'
        '    xmlns:aapt="http://schemas.android.com/aapt"\n'
        '    android:width="108dp"\n    android:height="108dp"\n    android:viewportWidth="108"\n    android:viewportHeight="108">\n'
        '    <path android:pathData="M0,0h108v108H0z">\n        <aapt:attr name="android:fillColor">\n'
        '            <gradient\n                android:type="linear"\n                android:startX="0"\n                android:startY="0"\n'
        '                android:endX="0"\n                android:endY="108">\n'
        '                <item android:offset="0" android:color="#FF312E81" />\n'
        '                <item android:offset="1" android:color="#FF1E1B4B" />\n'
        '            </gradient>\n        </aapt:attr>\n    </path>\n</vector>\n')
    st = path([cres, key])
    open(ROOT + 'android/app/src/main/res/drawable/ic_stat_app.xml', 'w', encoding='utf-8', newline='\n').write(
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<vector xmlns:android="http://schemas.android.com/apk/res/android"\n'
        '    android:width="24dp"\n    android:height="24dp"\n    android:viewportWidth="24"\n    android:viewportHeight="24">\n'
        '    <!-- The MoonPassword mark in one colour: monochrome status-bar icon. -->\n'
        '    <path\n        android:fillColor="#FFFFFFFF"\n        android:fillType="evenOdd"\n'
        '        android:pathData="' + st + '" />\n</vector>\n')
    # Windows: the mark as C# point lists (24x24 design space), shared by the tray and window icons
    def cs(name, poly):
        rows = [', '.join('%sf, %sf' % (fmt(x), fmt(y)) for x, y in poly[i:i + 6]) for i in range(0, len(poly), 6)]
        return '    public static readonly float[] ' + name + ' =\n    {\n' + ''.join('        ' + r + ',\n' for r in rows) + '    };\n'
    open(ROOT + 'windows/MoonPassword.Windows/Logic/BrandMark.cs', 'w', encoding='utf-8', newline='\n').write(
        'namespace MoonPassword.Windows.Logic;\n\n'
        '/// <summary>\n'
        '/// The MoonPassword mark (a crescent moon with a keyhole cut out) as polygons in a 24×24 design space,\n'
        '/// x, y pairs. Fill <see cref="Crescent"/> and <see cref="Keyhole"/> together with the even-odd rule: the keyhole\n'
        '/// lies inside the crescent and becomes a hole. Generated with the Android vectors and the macOS PNGs (docs/brand.md).\n'
        '/// </summary>\n'
        'public static class BrandMark\n{\n'
        '    /// <summary>Background gradient, top to bottom (#RRGGBB).</summary>\n'
        '    public const string BackgroundTop = "#312E81";\n    public const string BackgroundBottom = "#1E1B4B";\n\n'
        '    /// <summary>Colour of the moon.</summary>\n    public const string Moon = "#F8F4E3";\n\n'
        + cs('Crescent', cres) + '\n' + cs('Keyhole', key) + '}\n')
    print('emitted')
