"""Render the MoliSync brand icon to PNG and ICO.

The geometry mirrors client-android/app/src/main/res/drawable/ic_launcher_foreground.xml
(108 x 108 viewport). Run from the repository root:

    python assets/branding/render_icon.py

Requires Pillow. Outputs:
  assets/branding/molisync-icon-512.png
  assets/branding/molisync-icon-1024.png
  client-windows/src/MoliSync.Client/app.ico
"""

from pathlib import Path

from PIL import Image, ImageDraw

CREAM = (255, 249, 233, 255)
GREEN = (23, 61, 54, 255)
GREEN_LINE = (52, 125, 104, 255)

VIEWBOX = 108.0
SUPERSAMPLE = 8  # draw large, then downscale for antialiasing


def cubic(points, samples=96):
    (x0, y0), (x1, y1), (x2, y2), (x3, y3) = points
    result = []
    for index in range(samples + 1):
        t = index / samples
        u = 1 - t
        x = u**3 * x0 + 3 * u**2 * t * x1 + 3 * u * t**2 * x2 + t**3 * x3
        y = u**3 * y0 + 3 * u**2 * t * y1 + 3 * u * t**2 * y2 + t**3 * y3
        result.append((x, y))
    return result


def stroke(draw, points, width, fill):
    radius = width / 2
    draw.line(points, fill=fill, width=int(width), joint="curve")
    for x, y in (points[0], points[-1]):
        draw.ellipse([x - radius, y - radius, x + radius, y + radius], fill=fill)


def render(size):
    scale = size / VIEWBOX
    canvas = Image.new("RGBA", (size, size), GREEN)
    draw = ImageDraw.Draw(canvas)

    def px(x, y):
        return x * scale, y * scale

    top_arc = [(29, 48), (30, 37), (39, 29), (50, 29)]
    bottom_arc = [(79, 60), (78, 71), (69, 79), (58, 79)]
    stroke(draw, [px(*p) for p in cubic(top_arc)] + [px(72, 29)], 7 * scale, CREAM)
    stroke(draw, [px(*p) for p in cubic(bottom_arc)] + [px(36, 79)], 7 * scale, CREAM)

    draw.polygon([px(71, 21), px(84, 29), px(71, 37)], fill=CREAM)
    draw.polygon([px(37, 71), px(24, 79), px(37, 87)], fill=CREAM)

    draw.rounded_rectangle([*px(37, 39), *px(67, 70)], radius=6 * scale, fill=CREAM)
    draw.rounded_rectangle([*px(47, 47), *px(58, 50)], radius=1.5 * scale, fill=GREEN_LINE)
    draw.rounded_rectangle([*px(47, 54), *px(58, 57)], radius=1.5 * scale, fill=GREEN_LINE)
    draw.rounded_rectangle([*px(47, 61), *px(55, 64)], radius=1.5 * scale, fill=GREEN_LINE)

    return canvas


def main():
    root = Path(__file__).resolve().parents[2]
    branding = root / "assets" / "branding"

    master = render(int(VIEWBOX * SUPERSAMPLE * 8))
    for output_size in (512, 1024):
        master.resize((output_size, output_size), Image.LANCZOS).save(
            branding / f"molisync-icon-{output_size}.png"
        )

    ico_sizes = [16, 24, 32, 48, 64, 128, 256]
    icons = [master.resize((s, s), Image.LANCZOS) for s in ico_sizes]
    icons[-1].save(
        root / "client-windows" / "src" / "MoliSync.Client" / "app.ico",
        format="ICO",
        sizes=[(s, s) for s in ico_sizes],
        append_images=icons[:-1],
    )

    print("rendered brand icon assets")


if __name__ == "__main__":
    main()
