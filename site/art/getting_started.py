#!/usr/bin/env python3
"""Hand-drawn style illustrations for the Getting started page.

Writes the SVGs into the directory given as argument:

    python3 site/art/getting_started.py docs/img/getting-started

Strokes are drawn twice with seeded jitter (the rough.js look), so the output is deterministic.
"""
import math
import random
import sys
from pathlib import Path

OUT = Path(sys.argv[1])
OUT.mkdir(parents=True, exist_ok=True)

STYLE = """
<style>
:root {
  --ink:#1f2328; --muted:#6e7781; --paper:#ffffff;
  --acc:#6639ba; --acc-t:#efe8fb;
  --old:#b3261e; --old-t:#fde8e6;
  --new:#0f6f5c; --new-t:#ddf3ec;
  --man:#9a6700; --man-t:#fff1cc;
  --sky:#f3f6fa;
}
@media (prefers-color-scheme: dark) {
  :root {
    --ink:#e6edf3; --muted:#8b949e; --paper:#0d1117;
    --acc:#b083f0; --acc-t:#2a1f3d;
    --old:#ff7b72; --old-t:#3a1d1c;
    --new:#56d4a4; --new-t:#10302a;
    --man:#e3b341; --man-t:#2e2410;
    --sky:#161b22;
  }
}
path, line, polyline { fill:none; stroke-linecap:round; stroke-linejoin:round }
.s   { stroke:var(--ink); stroke-width:2.2 }
.sm  { stroke:var(--muted); stroke-width:1.6 }
.sa  { stroke:var(--acc); stroke-width:2.4 }
.so  { stroke:var(--old); stroke-width:2.2 }
.sn  { stroke:var(--new); stroke-width:2.4 }
.sx  { stroke:var(--man); stroke-width:2.2 }
.thin { stroke-width:1.4 }
.fat  { stroke-width:3.4 }
.f-paper { fill:var(--paper); stroke:none }
.f-sky   { fill:var(--sky); stroke:none }
.f-acc   { fill:var(--acc-t); stroke:none }
.f-old   { fill:var(--old-t); stroke:none }
.f-new   { fill:var(--new-t); stroke:none }
.f-man   { fill:var(--man-t); stroke:none }
.f-ink   { fill:var(--ink); stroke:none }
.f-acc-solid { fill:var(--acc); stroke:none }
.hatch-acc line { stroke:var(--acc); stroke-width:1.3; opacity:.45 }
.hatch-new line { stroke:var(--new); stroke-width:1.3; opacity:.45 }
.hatch-old line { stroke:var(--old); stroke-width:1.3; opacity:.4 }
.hatch-man line { stroke:var(--man); stroke-width:1.3; opacity:.45 }
text { font-family:"Chalkboard SE","Segoe Print","Comic Neue","Bradley Hand",system-ui,sans-serif; fill:var(--ink) }
.t   { font-size:19px }
.tb  { font-size:22px; font-weight:700 }
.ts  { font-size:15px }
.tm  { font-size:16px; fill:var(--muted) }
.ta  { fill:var(--acc) }
.to  { fill:var(--old) }
.tn  { fill:var(--new) }
.tx  { fill:var(--man) }
.mono { font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; font-size:13px }
.badge { fill:#fff; font-family:system-ui,-apple-system,"Segoe UI",sans-serif; font-weight:800; font-size:20px; text-anchor:middle }
@media (prefers-color-scheme: dark) { .badge { fill:#0d1117 } }
</style>
"""


class Pic:
    def __init__(self, w, h, title, desc, seed):
        self.w, self.h = w, h
        self.title, self.desc = title, desc
        self.rnd = random.Random(seed)
        self.parts = []

    # ---- primitives -------------------------------------------------------
    def j(self, a):
        return self.rnd.uniform(-a, a)

    def _stroke(self, pts, cls, amp, closed=False):
        """One jittered pass through pts, as quadratic segments with wobbly midpoints."""
        p = [(x + self.j(amp), y + self.j(amp)) for x, y in pts]
        d = f"M{p[0][0]:.1f},{p[0][1]:.1f}"
        seq = p[1:] + ([p[0]] if closed else [])
        prev = p[0]
        for x, y in seq:
            mx, my = (prev[0] + x) / 2, (prev[1] + y) / 2
            seg = math.hypot(x - prev[0], y - prev[1])
            a = min(2.2, seg * 0.02)
            d += f" Q{mx + self.j(a):.1f},{my + self.j(a):.1f} {x:.1f},{y:.1f}"
            prev = (x, y)
        self.parts.append(f'<path class="{cls}" d="{d}"/>')

    def poly(self, pts, cls="s", closed=False, amp=1.4, passes=2):
        for _ in range(passes):
            self._stroke(pts, cls, amp, closed)

    def line(self, x1, y1, x2, y2, cls="s", amp=1.2, passes=2):
        self.poly([(x1, y1), (x2, y2)], cls, False, amp, passes)

    def fill_poly(self, pts, fcls):
        d = "M" + " L".join(f"{x + self.j(1):.1f},{y + self.j(1):.1f}" for x, y in pts) + " Z"
        self.parts.append(f'<path class="{fcls}" d="{d}"/>')

    def rect(self, x, y, w, h, cls="s", fill=None, hatch=None, amp=1.4):
        pts = [(x, y), (x + w, y), (x + w, y + h), (x, y + h)]
        if fill:
            self.fill_poly(pts, fill)
        if hatch:
            self.hatch(x, y, w, h, hatch)
        # Each side separately, overshooting a little, like a pen lifted at corners.
        for (ax, ay), (bx, by) in zip(pts, pts[1:] + pts[:1]):
            dx, dy = bx - ax, by - ay
            L = math.hypot(dx, dy) or 1
            ox, oy = dx / L * 3, dy / L * 3
            self.line(ax - ox, ay - oy, bx + ox * .6, by + oy * .6, cls, amp)

    def hatch(self, x, y, w, h, cls, gap=9):
        clip = f"h{self.rnd.randint(0, 1 << 30)}"
        self.parts.append(f'<clipPath id="{clip}"><rect x="{x}" y="{y}" width="{w}" height="{h}"/></clipPath>')
        g = [f'<g class="{cls}" clip-path="url(#{clip})">']
        k = -h
        while k < w:
            g.append(f'<line x1="{x + k + self.j(1):.1f}" y1="{y + h:.1f}" x2="{x + k + h * .8 + self.j(1):.1f}" y2="{y:.1f}"/>')
            k += gap
        g.append("</g>")
        self.parts.append("".join(g))

    def ellipse(self, cx, cy, rx, ry, cls="s", fill=None, passes=2, n=26):
        if fill:
            pts = [(cx + rx * math.cos(2 * math.pi * i / n), cy + ry * math.sin(2 * math.pi * i / n)) for i in range(n)]
            self.fill_poly(pts, fill)
        for k in range(passes):
            start = self.rnd.uniform(0, 2 * math.pi)
            sweep = 2 * math.pi * 1.06  # overshoot: a hand-drawn circle does not close exactly
            pts = []
            for i in range(n + 2):
                t = start + sweep * i / (n + 1)
                r = 1 + self.j(.035)
                pts.append((cx + rx * r * math.cos(t), cy + ry * r * math.sin(t)))
            self._stroke(pts, cls, .6)

    def circle(self, cx, cy, r, cls="s", fill=None, passes=2):
        self.ellipse(cx, cy, r, r, cls, fill, passes)

    def arrow(self, pts, cls="s", head=12):
        """Polyline through pts, smoothed, with an open arrowhead at the end."""
        self.poly(pts, cls, amp=1.0)
        (ax, ay), (bx, by) = pts[-2], pts[-1]
        ang = math.atan2(by - ay, bx - ax)
        for s in (+1, -1):
            hx = bx - head * math.cos(ang + s * .45)
            hy = by - head * math.sin(ang + s * .45)
            self.line(hx, hy, bx, by, cls, amp=.6)

    def curve_arrow(self, x1, y1, cx, cy, x2, y2, cls="s", head=12, n=12):
        pts = []
        for i in range(n + 1):
            t = i / n
            pts.append(((1 - t) ** 2 * x1 + 2 * (1 - t) * t * cx + t * t * x2,
                        (1 - t) ** 2 * y1 + 2 * (1 - t) * t * cy + t * t * y2))
        self.arrow(pts, cls, head)

    def text(self, x, y, s, cls="t", anchor="start", rot=0):
        tr = f' transform="rotate({rot} {x} {y})"' if rot else ""
        s = s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
        self.parts.append(f'<text class="{cls}" x="{x}" y="{y}" text-anchor="{anchor}"{tr}>{s}</text>')

    # ---- composite doodles ------------------------------------------------
    def sheet(self, x, y, w=62, h=78, tone="paper", label=None, lcls="ts", rot=0, lines=4):
        """A sheet of paper with a folded corner and scribbled lines."""
        cls = {"paper": "s", "old": "so", "new": "sn", "acc": "sa", "man": "sx"}[tone]
        fill = {"paper": "f-paper", "old": "f-old", "new": "f-new", "acc": "f-acc", "man": "f-man"}[tone]
        if rot:
            self.parts.append(f'<g transform="rotate({rot} {x + w / 2} {y + h / 2})">')
        f = 14
        pts = [(x, y), (x + w - f, y), (x + w, y + f), (x + w, y + h), (x, y + h)]
        self.fill_poly(pts, fill)
        self.poly(pts, cls, closed=True, amp=1.1)
        self.poly([(x + w - f, y), (x + w - f, y + f), (x + w, y + f)], cls + " thin", amp=.6, passes=1)
        for i in range(lines):
            ly = y + 18 + i * 13
            if ly > y + h - 8:
                break
            lw = (w - 20) * self.rnd.uniform(.55, 1)
            self.scribble(x + 9, ly, lw, cls + " thin")
        if rot:
            self.parts.append("</g>")
        if label:
            self.text(x + w / 2, y + h + 20, label, lcls, "middle")

    def scribble(self, x, y, w, cls):
        pts = []
        n = max(3, int(w / 7))
        for i in range(n + 1):
            pts.append((x + w * i / n, y + (1.6 if i % 2 else -1.6)))
        self.poly(pts, cls, amp=.5, passes=1)

    def badge(self, cx, cy, n, r=19):
        self.ellipse(cx, cy, r, r, "sa", fill="f-acc-solid", passes=1)
        self.text(cx, cy + 7, str(n), "badge", "middle")

    def check(self, x, y, s=1.0, cls="sn fat"):
        self.poly([(x, y), (x + 7 * s, y + 8 * s), (x + 20 * s, y - 10 * s)], cls, amp=.6)

    def cross(self, x, y, s=1.0, cls="so fat"):
        self.line(x, y, x + 14 * s, y + 14 * s, cls, .5)
        self.line(x + 14 * s, y, x, y + 14 * s, cls, .5)

    def warn(self, x, y, s=1.0):
        pts = [(x, y + 18 * s), (x + 10 * s, y), (x + 20 * s, y + 18 * s)]
        self.fill_poly(pts, "f-man")
        self.poly(pts, "sx", closed=True, amp=.6)
        self.line(x + 10 * s, y + 6 * s, x + 10 * s, y + 11 * s, "sx", .3, 1)
        self.circle(x + 10 * s, y + 14.5 * s, .8 * s, "sx", passes=1)

    def gear(self, cx, cy, r, teeth=8, cls="sa", fill="f-acc"):
        pts = []
        for i in range(teeth * 4):
            a = 2 * math.pi * i / (teeth * 4)
            rr = r if (i % 4) in (0, 1) else r * .78
            pts.append((cx + rr * math.cos(a), cy + rr * math.sin(a)))
        self.fill_poly(pts, fill)
        self.poly(pts, cls, closed=True, amp=.7)
        self.circle(cx, cy, r * .3, cls)

    def person(self, x, y, s=1.0, cls="s", wave=False):
        """Stick figure; (x, y) is the top of the head."""
        self.circle(x, y + 13 * s, 13 * s, cls, fill="f-paper")
        self.line(x, y + 26 * s, x, y + 70 * s, cls)
        self.line(x, y + 70 * s, x - 15 * s, y + 104 * s, cls)
        self.line(x, y + 70 * s, x + 15 * s, y + 104 * s, cls)
        self.line(x, y + 38 * s, x + 30 * s, y + 52 * s, cls)  # arm holding something
        if wave:
            self.line(x, y + 38 * s, x - 24 * s, y + 18 * s, cls)
        else:
            self.line(x, y + 38 * s, x - 22 * s, y + 58 * s, cls)
        # face
        self.circle(x - 4 * s, y + 11 * s, .9 * s, cls, passes=1)
        self.circle(x + 5 * s, y + 11 * s, .9 * s, cls, passes=1)
        self.poly([(x - 5 * s, y + 17 * s), (x, y + 20 * s), (x + 6 * s, y + 17 * s)], cls + " thin", amp=.3, passes=1)

    def control_plane(self, x, y, w=170, h=150, label="control plane", tone="acc"):
        """A server box holding a little mesh of connected nodes."""
        cls, fill = ("sa", "f-acc") if tone == "acc" else ("sn", "f-new")
        self.rect(x, y, w, h, cls, fill=fill)
        self.line(x, y + 30, x + w, y + 30, cls + " thin", 1, 1)
        for i in range(3):
            self.circle(x + 16 + i * 14, y + 15, 4, cls, passes=1)
        nodes = [(x + w * .25, y + 65), (x + w * .62, y + 55), (x + w * .8, y + 100),
                 (x + w * .45, y + 115), (x + w * .2, y + 112)]
        for a, b in [(0, 1), (1, 2), (2, 3), (3, 4), (4, 0), (0, 3), (1, 3)]:
            self.line(*nodes[a], *nodes[b], "sm thin", .8, 1)
        for nx, ny in nodes:
            self.circle(nx, ny, 9, cls, fill="f-paper")
        if label:
            self.text(x + w / 2, y + h + 26, label, "t", "middle")

    def folder(self, x, y, w, h, label, tone="paper"):
        cls, fill = {"paper": ("s", "f-paper"), "new": ("sn", "f-new"), "man": ("sx", "f-man"), "acc": ("sa", "f-acc")}[tone]
        tab = [(x, y + 12), (x, y), (x + w * .38, y), (x + w * .45, y + 12)]
        body = [(x, y + 12), (x + w, y + 12), (x + w, y + h), (x, y + h)]
        self.fill_poly(tab + [(x + w * .45, y + 12)], fill)
        self.fill_poly(body, fill)
        self.poly(tab, cls, amp=.8)
        self.poly(body, cls, closed=True, amp=1.1)
        self.text(x + 12, y + 36, label, "mono")

    def svg(self):
        body = "\n".join(self.parts)
        return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {self.w} {self.h}" role="img">\n'
                f"<title>{self.title}</title>\n<desc>{self.desc}</desc>\n{STYLE}\n{body}\n</svg>\n")

    def save(self, name):
        (OUT / name).write_text(self.svg())


# ---------------------------------------------------------------------------
# Hero: the journey from today's mesh to the target version.
p = Pic(980, 350, "From your mesh today to 2.14 or 3.0 in three steps",
        "A winding path leads from a control plane holding old-style resources, past three stops "
        "labelled extract, migrate and apply, to a control plane holding the new resources.", 11)

# left island: today
p.ellipse(120, 262, 118, 22, "sm thin", fill="f-sky", passes=1)
p.control_plane(45, 85, 150, 128, label=None)
p.sheet(160, 150, 46, 58, "old", rot=-10, lines=3)
p.sheet(26, 160, 46, 58, "old", rot=8, lines=3)
p.text(120, 314, "your mesh today", "tb", "middle")
p.text(120, 338, "2.x, old APIs", "tm", "middle")
p.text(120, 50, "TrafficRoute, sources:,", "tm", "middle")
p.text(120, 70, "MeshSubset, from[] …", "tm", "middle")

# path
path_pts = []
for i in range(60):
    t = i / 59
    x = 240 + t * 500
    y = 205 + 38 * math.sin(t * math.pi * 2.0)
    path_pts.append((x, y))
# dashed footpath: short strokes
for i in range(0, len(path_pts) - 1, 2):
    p.line(*path_pts[i], *path_pts[i + 1], "sm", .4, 1)

stops = [(330, "extract", "read what runs"), (490, "migrate", "rewrite + report"), (650, "apply", "you, step by step")]
for n, (sx, word, sub) in enumerate(stops, 1):
    t = (sx - 240) / 500
    sy = 205 + 38 * math.sin(t * math.pi * 2.0)
    # signpost
    p.line(sx, sy, sx, sy - 98, "s", .8)
    p.rect(sx - 58, sy - 150, 116, 52, "sa", fill="f-paper")
    p.text(sx + 10, sy - 118, word, "tb ta", "middle")
    p.badge(sx - 58, sy - 150, n, r=16)
    p.text(sx, sy + 34, sub, "tm", "middle")

# little walker at the start of the path
p.person(262, 108, .82, "s", wave=True)

# right island: target
p.ellipse(860, 262, 118, 22, "sm thin", fill="f-sky", passes=1)
p.control_plane(785, 85, 150, 128, label=None, tone="new")
p.sheet(750, 150, 46, 58, "new", rot=-8, lines=3)
p.sheet(905, 155, 46, 58, "new", rot=9, lines=3)
p.check(838, 30, 1.6)
p.text(860, 314, "your mesh, migrated", "tb", "middle")
p.text(860, 338, "2.14 or 3.0", "tm", "middle")
p.text(860, 70, "MeshTimeout, targetRef …", "tm", "middle")
p.save("journey.svg")

# ---------------------------------------------------------------------------
# Step 1: extract.
p = Pic(800, 350, "Extract: copy every resource out of the control plane into YAML files",
        "kuma-migrator reads the control plane through kubectl or kumactl and copies each resource "
        "into a folder tree on your machine. A padlock sign says the control plane is only read. "
        "With --preflight, an amber sheet in the context folder is the 3.0 readiness audit report.", 21)

p.control_plane(40, 70, 190, 160, "your control plane")
# read-only sign
p.rect(70, 14, 132, 38, "sx", fill="f-man")
p.text(145, 40, "read only", "t tx", "middle")
p.rect(80, 26, 16, 13, "sx thin", fill="f-paper")
p.poly([(83, 26), (83, 21), (85, 18), (91, 18), (93, 21), (93, 26)], "sx thin", amp=.2, passes=1)

# flying sheets along arrows
p.curve_arrow(245, 110, 360, 40, 490, 105, "sa")
p.curve_arrow(245, 175, 370, 175, 490, 175, "sa")
p.curve_arrow(245, 235, 360, 300, 490, 245, "sa")
p.sheet(332, 30, 44, 56, "old", rot=-12, lines=3)
p.sheet(352, 140, 44, 56, "paper", rot=6, lines=3)
p.sheet(330, 238, 44, 56, "old", rot=14, lines=3)
p.text(368, 128, "kubectl", "mono ta", "middle")
p.text(368, 232, "or kumactl", "mono ta", "middle")

# folder tree
p.folder(500, 45, 285, 240, "raw-policies/")
p.folder(520, 100, 250, 52, "<ctx>-zone-ctx/", "paper")
p.folder(540, 165, 230, 52, "mesh-default/", "paper")
p.folder(540, 228, 230, 46, "global-scoped-resources/", "paper")
for i in range(4):
    p.sheet(680 + i * 20, 176, 18, 24, "paper", lines=1)
p.text(642, 315, "one YAML file per resource", "ts", "middle")
# optional: the 3.0 readiness audit report (extract --preflight)
p.sheet(728, 108, 26, 34, "man", lines=1)
p.text(642, 340, "+ v3-preflight.html with --preflight", "ts tx", "middle")
p.save("extract.svg")

# ---------------------------------------------------------------------------
# Step 2: migrate.
p = Pic(800, 330, "Migrate: a dry run first, then rewrite every file and write a report",
        "Old-style YAML files go into a machine with gears. New-style files come out on the other "
        "side, along with a clipboard report of ticks, warnings and errors. Glasses above the "
        "machine stand for the --dry-run preview.", 31)

# input pile
p.sheet(28, 120, 58, 74, "old", rot=-8)
p.sheet(62, 140, 58, 74, "old", rot=6)
p.text(84, 250, "old API", "t to", "middle")
p.text(84, 272, "sources: / from[]", "mono to", "middle")
p.arrow([(140, 170), (205, 170)], "s")

# machine
p.rect(215, 85, 250, 170, "sa", fill="f-acc")
p.poly([(270, 85), (290, 50), (390, 50), (410, 85)], "sa", amp=.8)  # hopper
p.fill_poly([(270, 85), (290, 50), (390, 50), (410, 85)], "f-paper")
p.poly([(270, 85), (290, 50), (390, 50), (410, 85)], "sa", amp=.8)
p.gear(285, 160, 38, 9)
p.gear(352, 196, 28, 7)
p.gear(405, 145, 24, 6)
p.text(340, 245, "kuma-migrator migrate", "mono ta", "middle")
# dry-run glasses on top
p.circle(318, 26, 15, "s")
p.circle(362, 26, 15, "s")
p.line(333, 26, 347, 26, "s", .4)
p.text(392, 32, "--dry-run: look first", "ts", "start")

# outputs
p.arrow([(475, 140), (540, 120)], "s")
p.sheet(552, 74, 58, 74, "new", rot=-5)
p.sheet(590, 92, 58, 74, "new", rot=7)
p.text(640, 196, "new API: targetRef, rules[]", "mono tn", "middle")
p.arrow([(475, 215), (545, 250)], "s")
# clipboard report
p.rect(555, 222, 120, 100, "s", fill="f-paper")
p.rect(593, 214, 44, 14, "s", fill="f-sky")
p.check(568, 248, .7)
p.scribble(590, 246, 66, "sm thin")
p.warn(566, 266, .8)
p.scribble(590, 276, 66, "sm thin")
p.cross(569, 294, .75)
p.scribble(590, 302, 66, "sm thin")
p.text(745, 262, "report:", "t", "middle")
p.text(745, 284, "what changed,", "ts", "middle")
p.text(745, 304, "what is left", "ts", "middle")
p.save("migrate.svg")

# ---------------------------------------------------------------------------
# Step 3: apply.
p = Pic(800, 330, "Apply: follow the report's checklist, finish the manual items, then clean up",
        "A person holds the report's Apply Checklist and applies the files to the control plane in "
        "order, Mesh last. A separate box marked _manual holds documents to finish by hand, next "
        "to a wrench.", 41)

p.person(70, 105, 1.05)
# clipboard in hand
p.rect(98, 132, 124, 118, "s", fill="f-paper")
p.rect(142, 125, 36, 13, "s", fill="f-sky")
items = ["policies", "gateways", "Mesh, last", "delete old"]
for i, it in enumerate(items):
    yy = 160 + i * 24
    p.rect(106, yy - 10, 12, 12, "s thin")
    if i < 2:
        p.check(105, yy - 4, .55, "sn")
    p.text(124, yy + 1, it, "ts")
p.text(160, 290, "Apply Checklist", "t", "middle")

# arrows into control plane
p.curve_arrow(235, 165, 315, 120, 395, 140, "sn")
p.curve_arrow(235, 205, 315, 215, 395, 200, "sn")
p.text(315, 105, "kubectl apply", "mono tn", "middle")
p.text(315, 248, "or kumactl apply", "mono tn", "middle")
p.control_plane(410, 80, 170, 150, "your control plane", tone="new")
p.check(468, 40, 1.5)

# manual box
p.rect(615, 140, 160, 120, "sx", fill="f-man", hatch="hatch-man")
p.rect(630, 116, 70, 30, "sx", fill="f-paper")
p.text(665, 137, "_manual/", "mono tx", "middle")
p.sheet(660, 165, 44, 56, "man", rot=-6, lines=3)
# wrench
p.poly([(728, 244), (756, 196)], "s fat", amp=.5)
p.poly([(752, 202), (746, 190), (749, 178), (759, 172), (761, 184), (768, 188), (776, 182), (775, 194), (765, 203), (752, 202)], "s", amp=.4)
p.text(695, 290, "finish by hand", "t tx", "middle")
p.save("apply.svg")
print("ok")
