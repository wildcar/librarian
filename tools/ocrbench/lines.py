"""Turn word/line boxes from a detector into reading-order text.

Boxes whose vertical centres are within half a line height of each other form
one row; rows are joined top to bottom, boxes within a row left to right. This
keeps table rows on one line, which is what the fact matcher needs.
"""


def to_text(items: list[tuple[str, tuple[float, float, float, float]]]) -> str:
    """items: (text, (x0, y0, x1, y1))."""
    items = [(t, b) for t, b in items if t and t.strip()]
    if not items:
        return ""
    items.sort(key=lambda it: (it[1][1] + it[1][3]) / 2)
    rows: list[list] = []
    for text, box in items:
        cy = (box[1] + box[3]) / 2
        h = max(1.0, box[3] - box[1])
        if rows:
            last = rows[-1]
            lcy = sum((b[1] + b[3]) / 2 for _, b in last) / len(last)
            lh = sum(b[3] - b[1] for _, b in last) / len(last)
            if abs(cy - lcy) < 0.5 * max(h, lh):
                last.append((text, box))
                continue
        rows.append([(text, box)])
    return "\n".join(
        " ".join(t for t, _ in sorted(r, key=lambda it: it[1][0])) for r in rows)
